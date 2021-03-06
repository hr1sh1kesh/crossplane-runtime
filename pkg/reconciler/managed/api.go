/*
Copyright 2019 The Crossplane Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package managed

import (
	"context"

	"github.com/pkg/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/crossplane/crossplane-runtime/pkg/meta"
	"github.com/crossplane/crossplane-runtime/pkg/resource"
)

// Error strings.
const (
	errCreateOrUpdateSecret = "cannot create or update connection secret"
	errUpdateManaged        = "cannot update managed resource"
	errUpdateManagedStatus  = "cannot update managed resource status"
)

// An APIFinalizer adds and removes finalizers to and from a resource.
type APIFinalizer struct {
	client    client.Client
	finalizer string
}

// NewAPIFinalizer returns a new APIFinalizer.
func NewAPIFinalizer(c client.Client, finalizer string) *APIFinalizer {
	return &APIFinalizer{client: c, finalizer: finalizer}
}

// AddFinalizer to the supplied Managed resource.
func (a *APIFinalizer) AddFinalizer(ctx context.Context, mg resource.Managed) error {
	if meta.FinalizerExists(mg, a.finalizer) {
		return nil
	}
	meta.AddFinalizer(mg, a.finalizer)
	return errors.Wrap(a.client.Update(ctx, mg), errUpdateManaged)
}

// RemoveFinalizer from the supplied Managed resource.
func (a *APIFinalizer) RemoveFinalizer(ctx context.Context, mg resource.Managed) error {
	meta.RemoveFinalizer(mg, a.finalizer)
	return errors.Wrap(resource.IgnoreNotFound(a.client.Update(ctx, mg)), errUpdateManaged)
}

// NameAsExternalName writes the name of the managed resource to
// the external name annotation field in order to be used as name of
// the external resource in provider.
type NameAsExternalName struct{ client client.Client }

// NewNameAsExternalName returns a new NameAsExternalName.
func NewNameAsExternalName(c client.Client) *NameAsExternalName {
	return &NameAsExternalName{client: c}
}

// Initialize the given managed resource.
func (a *NameAsExternalName) Initialize(ctx context.Context, mg resource.Managed) error {
	if meta.GetExternalName(mg) != "" {
		return nil
	}
	meta.SetExternalName(mg, mg.GetName())
	return errors.Wrap(a.client.Update(ctx, mg), errUpdateManaged)
}

// An APISecretPublisher publishes ConnectionDetails by submitting a Secret to a
// Kubernetes API server.
type APISecretPublisher struct {
	secret resource.Applicator
	typer  runtime.ObjectTyper
}

// NewAPISecretPublisher returns a new APISecretPublisher.
func NewAPISecretPublisher(c client.Client, ot runtime.ObjectTyper) *APISecretPublisher {
	// NOTE(negz): We transparently inject an APIPatchingApplicator in order to maintain
	// backward compatibility with the original API of this function.
	return &APISecretPublisher{secret: resource.NewAPIPatchingApplicator(c), typer: ot}
}

// PublishConnection publishes the supplied ConnectionDetails to a Secret in the
// same namespace as the supplied Managed resource. It is a no-op if the secret
// already exists with the supplied ConnectionDetails.
func (a *APISecretPublisher) PublishConnection(ctx context.Context, mg resource.Managed, c ConnectionDetails) error {
	// This resource does not want to expose a connection secret.
	if mg.GetWriteConnectionSecretToReference() == nil {
		return nil
	}

	s := resource.ConnectionSecretFor(mg, resource.MustGetKind(mg, a.typer))
	s.Data = c
	return errors.Wrap(a.secret.Apply(ctx, s, resource.ConnectionSecretMustBeControllableBy(mg.GetUID())), errCreateOrUpdateSecret)
}

// UnpublishConnection is no-op since PublishConnection only creates resources
// that will be garbage collected by Kubernetes when the managed resource is
// deleted.
func (a *APISecretPublisher) UnpublishConnection(ctx context.Context, mg resource.Managed, c ConnectionDetails) error {
	return nil
}
