package handlers_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"

	"github.com/cloudfoundry-incubator/rep/evacuation/evacuation_context/fake_evacuation_context"
	"github.com/cloudfoundry-incubator/rep/handlers"
	"github.com/pivotal-golang/lager/lagertest"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("EvacuationHandler", func() {
	Describe("ServeHTTP", func() {
		var (
			logger          *lagertest.TestLogger
			fakeEvacuatable *fake_evacuation_context.FakeEvacuatable
			handler         *handlers.EvacuationHandler

			responseRecorder *httptest.ResponseRecorder
			request          *http.Request
		)

		BeforeEach(func() {
			logger = lagertest.NewTestLogger("test")
			fakeEvacuatable = new(fake_evacuation_context.FakeEvacuatable)
			handler = handlers.NewEvacuationHandler(logger, fakeEvacuatable)
		})

		Context("when receiving a request", func() {
			BeforeEach(func() {
				responseRecorder = httptest.NewRecorder()

				var err error
				request, err = http.NewRequest("POST", "/evacuate", nil)
				Expect(err).NotTo(HaveOccurred())

				handler.ServeHTTP(responseRecorder, request)
			})

			It("starts evacuation", func() {
				Expect(fakeEvacuatable.EvacuateCallCount()).To(Equal(1))
			})

			It("responds with 202 ACCEPTED", func() {
				Expect(responseRecorder.Code).To(Equal(http.StatusAccepted))
			})

			It("returns the location of the Ping endpoint", func() {
				var responseValues map[string]string
				err := json.Unmarshal(responseRecorder.Body.Bytes(), &responseValues)
				Expect(err).NotTo(HaveOccurred())
				Expect(responseValues).To(HaveKey("ping_path"))
				Expect(responseValues["ping_path"]).To(Equal("/ping"))
			})
		})
	})
})
