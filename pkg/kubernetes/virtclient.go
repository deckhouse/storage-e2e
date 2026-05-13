package kubernetes

import (
	"context"

	"github.com/deckhouse/storage-e2e/internal/kubernetes/virtualization"
	"k8s.io/client-go/rest"
)

func New(ctx context.Context, config *rest.Config) (*virtualization.Client, error) {
	return virtualization.NewClient(ctx, config)
}
