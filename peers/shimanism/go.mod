// shimanism is the K8s-native peer for shim services without a
// good third-party OSS fit. Released independently of the main
// shim binary so operators can pin / deploy / upgrade it on its
// own cadence.

module github.com/e6qu/shimanism/peers/shimanism

go 1.26
