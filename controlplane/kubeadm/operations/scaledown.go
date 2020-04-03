package operations

import (
	"context"
	"time"

	"sigs.k8s.io/cluster-api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
)

//go:generate go run github.com/maxbrunsfeld/counterfeiter/v6 -generate

//counterfeiter:generate . WorkloadCluster
type WorkloadCluster interface {
}

//counterfeiter:generate . ControlPlane
type ControlPlane interface {
	HasDeletingMachine() bool
}

type ScaleDownOptions struct {
	DeleteRequeueAfter time.Duration
}

func ScaleDown(
	ctx context.Context,
	controlPlane ControlPlane,
	workloadCluster WorkloadCluster,
	opts ScaleDownOptions,
) (ctrl.Result, error) {
	if controlPlane.HasDeletingMachine() {
		return ctrl.Result{}, &errors.RequeueAfterError{RequeueAfter: opts.DeleteRequeueAfter}
	}
	/*
	 select the machine for deletion
	 annotate machine to be deleted
	 check if workload cluster etcd is healthy
	 forward workload cluster etcd leadership
	 remove machine from workload cluster etcd
	 remove machine from workload cluster kubeadm configmap
	 annotate that the machine has been removed
	 check if control plane is healthy
	 delete machine
	*/
	return ctrl.Result{}, nil
}
