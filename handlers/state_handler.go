package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/cloudfoundry-incubator/rep"
	"github.com/pivotal-golang/lager"
)

type state struct {
	rep    rep.AuctionCellClient
	logger lager.Logger
}

func (h *state) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	logger := h.logger.Session("auction-fetch-state")
	logger.Info("handling")

	state, err := h.rep.State()
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		logger.Error("failed-to-fetch-state", err)
		return
	}

	json.NewEncoder(w).Encode(state)
	logger.Info("success")
}
