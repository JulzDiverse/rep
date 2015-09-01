package rep_test

import (
	"errors"

	"github.com/cloudfoundry-incubator/auction/auctiontypes"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("State", func() {
	var state auctiontypes.CellState

	BeforeEach(func() {
		state = auctiontypes.CellState{
			RootFSProviders: auctiontypes.RootFSProviders{"docker": auctiontypes.ArbitraryRootFSProvider{}},
		}
	})

	It("should ask the rep for state", func() {
		client.State()
		Expect(auctionRep.StateCallCount()).To(Equal(1))
	})

	Context("when the request succeeds", func() {
		BeforeEach(func() {
			auctionRep.StateReturns(state, nil)
		})

		It("should return the state returned by the rep", func() {
			Expect(client.State()).To(Equal(state))
		})
	})

	Context("when the request fails", func() {
		BeforeEach(func() {
			auctionRep.StateReturns(state, errors.New("boom"))
		})

		It("should error", func() {
			state, err := client.State()
			Expect(state).To(BeZero())
			Expect(err).To(HaveOccurred())
		})
	})

	Context("when a request errors (in the network sense)", func() {
		It("should error", func() {
			state, err := clientForServerThatErrors.State()
			Expect(state).To(BeZero())
			Expect(err).To(HaveOccurred())
		})
	})
})
