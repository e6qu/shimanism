package restxml

import (
	"net/http"
	"sort"
	"strings"
)

// Router dispatches requests to per-operation http.Handlers using the
// Smithy URI template each operation declares.
//
// AWS REST-XML operations are disambiguated by (method, path-template,
// required-query-params). For example, on S3 the path "/{Bucket}" hosts
// CreateBucket (PUT), DeleteBucket (DELETE), HeadBucket (HEAD),
// ListObjectsV2 (GET with ?list-type=2), GetBucketVersioning
// (GET with ?versioning), and many others. The router compares the
// incoming request's method + path + query against each registered
// template and dispatches to the most-specific match.
//
// "Most specific" = the registered template with the largest set of
// required query parameters that the request satisfies. This is the
// AWS convention; the `?x-id=<Operation>` query param the AWS SDK
// adds to every request makes most disambiguation trivial.
type Router struct {
	routes []route
	// sortedOnce mirrors sync.Once but avoids the dep; we sort eagerly
	// on first ServeHTTP after each Register.
	dirty bool
}

type route struct {
	Method           string
	Path             string            // path portion of the template (no ?…)
	Query            map[string]string // required query params; "" = presence-only
	RequiredHeaders  []string          // header names that must be present
	RequiredQueries  []string          // query names that must be present (presence-only)
	ForbiddenQueries []string          // query names that, if present, disqualify this route
	Handler          http.Handler
	Operation        string // for diagnostics
}

// RouteOptions describes the disambiguation predicates a route uses
// beyond the (method, path, fixed-query) tuple extracted from a Smithy
// URI template. The router prefers more-specific matches; routes that
// declare extra required headers or query params win over routes that
// share the path but declare none.
type RouteOptions struct {
	// RequiredHeaders names HTTP headers that must be present.
	RequiredHeaders []string
	// RequiredQueries names URL query parameters that must be present
	// (presence is enough; the value is not pinned by the route).
	RequiredQueries []string
	// ForbiddenQueries names query parameters that, if present on the
	// incoming request, disqualify this route. Used by "base" object /
	// bucket operations (GetObject, HeadObject, DeleteObject,
	// PutObject) to reject S3 feature-config queries (`?tagging`,
	// `?acl`, `?policy`, …) that name out-of-intersection sibling
	// operations. Without this, a request like
	// `GET /bucket/key?tagging` falls through to GetObject and the
	// shim silently returns the object body — a fidelity break.
	ForbiddenQueries []string
}

// Register mounts a handler for an operation declared with the given
// HTTP method and Smithy URI template (e.g. "/{Bucket}/{Key+}?x-id=GetObject").
// Any "?…" suffix is parsed into the route's required query map.
//
// The "x-id" query parameter is dropped from the matching predicate.
// AWS SDKs add it as an operation-disambiguator; the AWS CLI's
// higher-level commands (e.g. `aws s3 cp`) do not. Removing it lets
// both targets share the same route.
func (r *Router) Register(method, template, operation string, h http.Handler, opts ...RouteOptions) {
	path, query := splitTemplate(template)
	delete(query, "x-id")
	var reqHeaders, reqQueries, forbiddenQueries []string
	for _, o := range opts {
		reqHeaders = append(reqHeaders, o.RequiredHeaders...)
		reqQueries = append(reqQueries, o.RequiredQueries...)
		forbiddenQueries = append(forbiddenQueries, o.ForbiddenQueries...)
	}
	r.routes = append(r.routes, route{
		Method:           method,
		Path:             path,
		Query:            query,
		RequiredHeaders:  reqHeaders,
		RequiredQueries:  reqQueries,
		ForbiddenQueries: forbiddenQueries,
		Handler:          h,
		Operation:        operation,
	})
	r.dirty = true
}

// ServeHTTP dispatches to the first matching route, with routes
// ordered by most-specific (most required-query-params) first.
func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if r.dirty {
		// Stable sort by descending route-specificity: routes with
		// more constraints (fixed query params + required headers +
		// required queries) win over less-specific siblings.
		sort.SliceStable(r.routes, func(i, j int) bool {
			a := len(r.routes[i].Query) + len(r.routes[i].RequiredHeaders) + len(r.routes[i].RequiredQueries)
			b := len(r.routes[j].Query) + len(r.routes[j].RequiredHeaders) + len(r.routes[j].RequiredQueries)
			return a > b
		})
		r.dirty = false
	}
	q := req.URL.Query()
	for _, rt := range r.routes {
		if rt.Method != req.Method {
			continue
		}
		if _, ok := MatchURI(req.URL.Path, rt.Path); !ok {
			continue
		}
		if !queryMatches(q, rt.Query) {
			continue
		}
		if !headersPresent(req.Header, rt.RequiredHeaders) {
			continue
		}
		if !queriesPresent(q, rt.RequiredQueries) {
			continue
		}
		if queriesPresentAny(q, rt.ForbiddenQueries) {
			continue
		}
		rt.Handler.ServeHTTP(w, req)
		return
	}
	WriteError(w, http.StatusNotFound, "InvalidRequest",
		"no shimmed operation matches "+req.Method+" "+req.URL.Path)
}

func splitTemplate(template string) (string, map[string]string) {
	path := template
	q := map[string]string{}
	if i := strings.IndexByte(template, '?'); i >= 0 {
		path = template[:i]
		for _, kv := range strings.Split(template[i+1:], "&") {
			if kv == "" {
				continue
			}
			if eq := strings.IndexByte(kv, '='); eq >= 0 {
				q[kv[:eq]] = kv[eq+1:]
			} else {
				q[kv] = ""
			}
		}
	}
	return path, q
}

func headersPresent(actual http.Header, required []string) bool {
	for _, name := range required {
		if actual.Get(name) == "" {
			return false
		}
	}
	return true
}

func queriesPresent(actual map[string][]string, required []string) bool {
	for _, name := range required {
		if got, ok := actual[name]; !ok || len(got) == 0 {
			return false
		}
	}
	return true
}

// queriesPresentAny reports whether at least one of `names` is
// present in `actual`. Used to short-circuit a route when any
// forbidden query is present.
func queriesPresentAny(actual map[string][]string, names []string) bool {
	for _, name := range names {
		if got, ok := actual[name]; ok && len(got) > 0 {
			return true
		}
	}
	return false
}

func queryMatches(actual map[string][]string, required map[string]string) bool {
	for k, want := range required {
		got, ok := actual[k]
		if !ok || len(got) == 0 {
			return false
		}
		if want == "" {
			// presence-only; any value counts.
			continue
		}
		// AWS spec query templates can carry multi-valued constraints;
		// the actual request must include the required value at least
		// once.
		matched := false
		for _, v := range got {
			if v == want {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	return true
}
