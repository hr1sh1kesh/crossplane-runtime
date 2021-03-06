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

package resource

import (
	"context"
	"encoding/json"

	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/crossplane/crossplane-runtime/pkg/meta"
)

// Error strings.
const (
	errGetSecret            = "cannot get managed resource's connection secret"
	errSecretConflict       = "cannot establish control of existing connection secret"
	errUpdateSecret         = "cannot update connection secret"
	errCreateOrUpdateSecret = "cannot create or update connection secret"
)

// An APIManagedConnectionPropagator propagates connection details by reading
// them from and writing them to a Kubernetes API server.
type APIManagedConnectionPropagator struct {
	client ClientApplicator
	typer  runtime.ObjectTyper
}

// NewAPIManagedConnectionPropagator returns a new APIManagedConnectionPropagator.
func NewAPIManagedConnectionPropagator(c client.Client, t runtime.ObjectTyper) *APIManagedConnectionPropagator {
	return &APIManagedConnectionPropagator{
		client: ClientApplicator{Client: c, Applicator: NewAPIUpdatingApplicator(c)},
		typer:  t,
	}
}

// PropagateConnection details from the supplied resource to the supplied claim.
func (a *APIManagedConnectionPropagator) PropagateConnection(ctx context.Context, o LocalConnectionSecretOwner, mg Managed) error {
	// Either this resource does not expose a connection secret, or this claim
	// does not want one.
	if mg.GetWriteConnectionSecretToReference() == nil || o.GetWriteConnectionSecretToReference() == nil {
		return nil
	}

	n := types.NamespacedName{
		Namespace: mg.GetWriteConnectionSecretToReference().Namespace,
		Name:      mg.GetWriteConnectionSecretToReference().Name,
	}
	from := &corev1.Secret{}
	if err := a.client.Get(ctx, n, from); err != nil {
		return errors.Wrap(err, errGetSecret)
	}

	// Make sure the managed resource is the controller of the connection secret
	// it references before we propagate it. This ensures a managed resource
	// cannot use Crossplane to circumvent RBAC by propagating a secret it does
	// not own.
	if c := metav1.GetControllerOf(from); c == nil || c.UID != mg.GetUID() {
		return errors.New(errSecretConflict)
	}

	to := LocalConnectionSecretFor(o, MustGetKind(o, a.typer))
	to.Data = from.Data

	meta.AllowPropagation(from, to)

	if err := a.client.Apply(ctx, to, ConnectionSecretMustBeControllableBy(o.GetUID())); err != nil {
		return errors.Wrap(err, errCreateOrUpdateSecret)
	}

	return errors.Wrap(a.client.Update(ctx, from), errUpdateSecret)
}

// An APIPatchingApplicator applies changes to an object by either creating or
// patching it in a Kubernetes API server.
type APIPatchingApplicator struct {
	client client.Client
}

// NewAPIPatchingApplicator returns an Applicator that applies changes to an
// object by either creating or patching it in a Kubernetes API server.
func NewAPIPatchingApplicator(c client.Client) *APIPatchingApplicator {
	return &APIPatchingApplicator{client: c}
}

// Apply changes to the supplied object. The object will be created if it does
// not exist, or patched if it does.
func (a *APIPatchingApplicator) Apply(ctx context.Context, o runtime.Object, ao ...ApplyOption) error {
	m, ok := o.(metav1.Object)
	if !ok {
		return errors.New("cannot access object metadata")
	}

	desired := o.DeepCopyObject()

	err := a.client.Get(ctx, types.NamespacedName{Name: m.GetName(), Namespace: m.GetNamespace()}, o)
	if kerrors.IsNotFound(err) {
		// TODO(negz): Apply ApplyOptions here too?
		return errors.Wrap(a.client.Create(ctx, o), "cannot create object")
	}
	if err != nil {
		return errors.Wrap(err, "cannot get object")
	}

	for _, fn := range ao {
		if err := fn(ctx, o, desired); err != nil {
			return err
		}
	}

	// TODO(negz): Allow callers to override the kind of patch used.
	return errors.Wrap(a.client.Patch(ctx, o, &patch{desired}), "cannot patch object")
}

type patch struct{ from runtime.Object }

func (p *patch) Type() types.PatchType                 { return types.MergePatchType }
func (p *patch) Data(_ runtime.Object) ([]byte, error) { return json.Marshal(p.from) }

// An APIUpdatingApplicator applies changes to an object by either creating or
// updating it in a Kubernetes API server.
type APIUpdatingApplicator struct {
	client client.Client
}

// NewAPIUpdatingApplicator returns an Applicator that applies changes to an
// object by either creating or updating it in a Kubernetes API server.
func NewAPIUpdatingApplicator(c client.Client) *APIUpdatingApplicator {
	return &APIUpdatingApplicator{client: c}
}

// Apply changes to the supplied object. The object will be created if it does
// not exist, or updated if it does.
func (a *APIUpdatingApplicator) Apply(ctx context.Context, o runtime.Object, ao ...ApplyOption) error {
	m, ok := o.(metav1.Object)
	if !ok {
		return errors.New("cannot access object metadata")
	}

	current := o.DeepCopyObject()

	err := a.client.Get(ctx, types.NamespacedName{Name: m.GetName(), Namespace: m.GetNamespace()}, current)
	if kerrors.IsNotFound(err) {
		// TODO(negz): Apply ApplyOptions here too?
		return errors.Wrap(a.client.Create(ctx, o), "cannot create object")
	}
	if err != nil {
		return errors.Wrap(err, "cannot get object")
	}

	for _, fn := range ao {
		if err := fn(ctx, current, o); err != nil {
			return err
		}
	}

	return errors.Wrap(a.client.Update(ctx, o), "cannot update object")
}
