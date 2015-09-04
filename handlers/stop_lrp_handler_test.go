package handlers_test

import (
	"bytes"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"net/url"

	"github.com/cloudfoundry-incubator/rep/handlers"
	"github.com/cloudfoundry-incubator/rep/lrp_stopper/fake_lrp_stopper"
	"github.com/pivotal-golang/lager"
	"github.com/pivotal-golang/lager/lagertest"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("StopLRPInstanceHandler", func() {
	var stopInstanceHandler *handlers.StopLRPInstanceHandler
	var fakeStopper *fake_lrp_stopper.FakeLRPStopper
	var resp *httptest.ResponseRecorder
	var req *http.Request

	BeforeEach(func() {
		var err error
		fakeStopper = &fake_lrp_stopper.FakeLRPStopper{}

		logger := lagertest.NewTestLogger("test")
		logger.RegisterSink(lager.NewWriterSink(GinkgoWriter, lager.DEBUG))

		stopInstanceHandler = handlers.NewStopLRPInstanceHandler(logger, fakeStopper)

		resp = httptest.NewRecorder()

		req, err = http.NewRequest("POST", "", nil)
		Expect(err).NotTo(HaveOccurred())
	})

	JustBeforeEach(func() {
		stopInstanceHandler.ServeHTTP(resp, req)
	})

	Context("when the request is valid", func() {
		var processGuid string
		var instanceGuid string

		BeforeEach(func() {
			processGuid = "process-guid"
			instanceGuid = "instance-guid"

			values := make(url.Values)
			values.Set(":process_guid", processGuid)
			values.Set(":instance_guid", instanceGuid)
			req.URL.RawQuery = values.Encode()
		})

		It("responds with 202 Accepted", func() {
			Expect(resp.Code).To(Equal(http.StatusAccepted))
		})

		It("eventually stops the instance", func() {
			Eventually(fakeStopper.StopInstanceCallCount).Should(Equal(1))

			processGuid, instanceGuid := fakeStopper.StopInstanceArgsForCall(0)
			Expect(processGuid).To(Equal(processGuid))
			Expect(instanceGuid).To(Equal(instanceGuid))
		})
	})

	Context("when the request is invalid", func() {
		BeforeEach(func() {
			req.Body = ioutil.NopCloser(bytes.NewBufferString("foo"))
		})

		It("responds with 400 Bad Request", func() {
			Expect(resp.Code).To(Equal(http.StatusBadRequest))
		})

		It("does not attempt to stop the instance", func() {
			Expect(fakeStopper.StopInstanceCallCount()).To(Equal(0))
		})
	})
})
