package healthcheck_test

import (
	"context"
	"testing"

	. "github.com/onsi/gomega"
	"github.com/pkg/errors"
	kerrors "k8s.io/apimachinery/pkg/util/errors"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1alpha3"
	. "sigs.k8s.io/cluster-api/test/helpers"
	. "sigs.k8s.io/cluster-api/util"
	. "sigs.k8s.io/cluster-api/util/healthcheck"
	. "sigs.k8s.io/cluster-api/util/healthcheck/healthcheckfakes"
)

func TestUnhealthyMachineDeletionRemediator(t *testing.T) {
	testCases := []struct {
		name string
		test func(g *WithT, client *FakeClient, r *UnhealthyMachineDeletionRemediator)
	}{
		{
			name: "it returns true when it finds any machines to delete",
			test: func(g *WithT, client *FakeClient, r *UnhealthyMachineDeletionRemediator) {
				machines := NewFilterableMachineCollection(
					Machine("one", WithAnnotation(clusterv1.MachineUnhealthy, "")),
					Machine("two", WithAnnotation(clusterv1.MachineUnhealthy, "")),
					Machine("three"),
				)

				unhealthyMachines := machines.Filter(func(m *clusterv1.Machine) bool {
					return m.Name == "one" || m.Name == "two"
				})

				ctx := context.Background()
				remediated, err := r.Remediate(ctx, machines)
				g.Expect(err).To(BeNil())
				g.Expect(remediated).To(BeTrue())

				g.Expect(client.DeleteCallCount()).To(Equal(2))

				actualCtx, machine1, args := client.DeleteArgsForCall(0)
				g.Expect(actualCtx).To(Equal(ctx))
				g.Expect(args).To(BeNil())

				actualCtx, machine2, args := client.DeleteArgsForCall(1)
				g.Expect(actualCtx).To(Equal(ctx))
				g.Expect(args).To(BeNil())

				g.Expect(unhealthyMachines).To(ConsistOf(machine1, machine2))
			},
		},
		{
			name: "it returns false if there are no machines in need of remediation",
			test: func(g *WithT, client *FakeClient, r *UnhealthyMachineDeletionRemediator) {
				machines := NewFilterableMachineCollection(
					Machine("one"),
					Machine("two"),
					Machine("three"),
				)

				remediated, err := r.Remediate(context.Background(), machines)
				g.Expect(err).To(BeNil())
				g.Expect(remediated).To(BeFalse())

				g.Expect(client.DeleteCallCount()).To(Equal(0))
			},
		},
		{
			name: "it returns any errors from deletion",
			test: func(g *WithT, client *FakeClient, r *UnhealthyMachineDeletionRemediator) {
				machines := NewFilterableMachineCollection(
					Machine("one", WithAnnotation(clusterv1.MachineUnhealthy, "")),
					Machine("two", WithAnnotation(clusterv1.MachineUnhealthy, "")),
					Machine("three"),
				)

				err := errors.New("whoops")
				client.DeleteReturnsOnCall(0, err)

				remediated, actual := r.Remediate(context.Background(), machines)
				g.Expect(kerrors.Reduce(actual)).To(Equal(err))
				g.Expect(remediated).To(BeTrue())
				g.Expect(client.DeleteCallCount()).To(Equal(2))
			},
		},
	}

	for _, tc := range testCases {
		g := NewWithT(t)
		client := &FakeClient{}
		r := NewUnhealthyMachineDeletionRemediator(client)
		tc.test(g, client, r)
	}
}
