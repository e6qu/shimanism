package main

import (
	"github.com/e6qu/shimanism/internal/rdbms/domain"
	"github.com/e6qu/shimanism/services/rdbms/backends/cnpg"
	"github.com/e6qu/shimanism/services/rdbms/backends/inmem"
)

func newInmemRDBMSBackend() domain.RDBMS {
	return inmem.New()
}

func newCNPGBackend(kubeconfig, namespace string) (domain.RDBMS, error) {
	dyn, core, err := k8sDynamicCore(kubeconfig)
	if err != nil {
		return nil, err
	}
	return cnpg.New(dyn, core, cnpg.Config{Namespace: namespace}), nil
}
