package handlers

import (
	"net/http"

	"github.com/cloudfoundry-incubator/rep"
	"github.com/pivotal-golang/lager"
)

type reset struct {
	rep    rep.AuctionCellClient
	logger lager.Logger
}

func (h *reset) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	logger := h.logger.Session("sim-reset")
	logger.Info("handling")

	simRep, ok := h.rep.(rep.SimClient)
	if !ok {
		w.WriteHeader(http.StatusInternalServerError)
		logger.Error("not-a-simulation-rep", nil)
		return
	}

	err := simRep.Reset()
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		logger.Error("failed-to-reset", err)
		return
	}
	logger.Info("success")
}
