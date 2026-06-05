package main

import (
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"

	"github.com/e6qu/shimanism/internal/cache/domain"
	azurebackend "github.com/e6qu/shimanism/services/cache/backends/azure"
)

func newAzureCacheBackend(subscription, resourceGroup, location string) (domain.Cache, error) {
	return newAzureManagedBackend(subscription, resourceGroup, location, cacheAzureConfig, cacheAzureBackend)
}

func cacheAzureConfig(subscription, resourceGroup, location string, cred azcore.TokenCredential) azurebackend.Config {
	return azurebackend.Config{
		SubscriptionID: subscription,
		ResourceGroup:  resourceGroup,
		Location:       location,
		Credential:     cred,
	}
}

func cacheAzureBackend(cfg azurebackend.Config) (domain.Cache, error) {
	return azurebackend.New(cfg)
}
