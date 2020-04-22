package healthcheck

import (
	"context"

	"k8s.io/apimachinery/pkg/runtime"
	kerrors "k8s.io/apimachinery/pkg/util/errors"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1alpha3"
	"sigs.k8s.io/cluster-api/util"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

//go:generate counterfeiter . Client
type Client interface {
	Delete(context.Context, runtime.Object, ...client.DeleteOption) error
}

func NewUnhealthyMachineDeletionRemediator(client Client) *UnhealthyMachineDeletionRemediator {
	return &UnhealthyMachineDeletionRemediator{
		client: client,
	}
}

type UnhealthyMachineDeletionRemediator struct {
	client Client
}

func (r *UnhealthyMachineDeletionRemediator) Remediate(ctx context.Context, machines util.FilterableMachineCollection) (bool, error) {
	var (
		errs    []error
		deleted bool
	)

	for _, machine := range machines {
		if _, unhealthy := machine.GetAnnotations()[clusterv1.MachineUnhealthy]; !unhealthy {
			continue
		}

		if err := r.client.Delete(ctx, machine); err != nil {
			errs = append(errs, err)
		}

		deleted = true
	}

	return deleted, kerrors.NewAggregate(errs)
}
