package operations_test

import (
	"context"
	"testing"
	"time"

	. "github.com/onsi/gomega"
	. "sigs.k8s.io/cluster-api/controlplane/kubeadm/operations"
	. "sigs.k8s.io/cluster-api/controlplane/kubeadm/operations/operationsfakes"
	"sigs.k8s.io/cluster-api/errors"
)

func TestScaleDown(t *testing.T) {
	tests := []struct {
		name string
		test func(*WithT)
	}{
		{
			name: "it requeues if the controlplane is already deleting another machine",
			test: func(g *WithT) {
				controlPlane := &FakeControlPlane{}
				controlPlane.HasDeletingMachineReturns(true)

				opts := ScaleDownOptions{
					DeleteRequeueAfter: time.Hour,
				}

				_, err := ScaleDown(context.Background(), controlPlane, &FakeWorkloadCluster{}, opts)
				g.Expect(err).To(Equal(&errors.RequeueAfterError{RequeueAfter: opts.DeleteRequeueAfter}))
			},
		},
		{
			name: "annotates the machine to be deleted",
			test: func(g *WithT) {
				controlPlane := &FakeControlPlane{}
				controlPlane.HasDeletingMachineReturns(true)

				opts := ScaleDownOptions{
					DeleteRequeueAfter: time.Hour,
				}

				_, err := ScaleDown(context.Background(), controlPlane, &FakeWorkloadCluster{}, opts)
				g.Expect(err).To(Equal(&errors.RequeueAfterError{RequeueAfter: opts.DeleteRequeueAfter}))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			tt.test(g)
		})
	}
}
