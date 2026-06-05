package main

import (
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"

	"github.com/e6qu/shimanism/internal/rdbms/domain"
	azurebackend "github.com/e6qu/shimanism/services/rdbms/backends/azure"
)

func newAzureRDBMSBackend(subscription, resourceGroup, location string) (domain.RDBMS, error) {
	return newAzureManagedBackend(subscription, resourceGroup, location, rdbmsAzureConfig, rdbmsAzureBackend)
}

func rdbmsAzureConfig(subscription, resourceGroup, location string, cred azcore.TokenCredential) azurebackend.Config {
	return azurebackend.Config{
		SubscriptionID: subscription,
		ResourceGroup:  resourceGroup,
		Location:       location,
		Credential:     cred,
	}
}

func rdbmsAzureBackend(cfg azurebackend.Config) (domain.RDBMS, error) {
	return azurebackend.New(cfg)
}
