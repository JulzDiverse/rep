package main_test

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/cloudfoundry-incubator/bbs"
	bbstestrunner "github.com/cloudfoundry-incubator/bbs/cmd/bbs/testrunner"
	"github.com/cloudfoundry-incubator/consuladapter"
	"github.com/cloudfoundry-incubator/consuladapter/consulrunner"
	"github.com/cloudfoundry/storeadapter/storerunner/etcdstorerunner"
	. "github.com/onsi/ginkgo"
	"github.com/onsi/ginkgo/config"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gexec"
	"github.com/onsi/gomega/ghttp"
	"github.com/tedsuo/ifrit"
	"github.com/tedsuo/ifrit/ginkgomon"
)

var cellID string
var representativePath string
var etcdRunner *etcdstorerunner.ETCDClusterRunner
var etcdPort, natsPort int
var serverPort int
var consulRunner *consulrunner.ClusterRunner
var consulSession *consuladapter.Session

var bbsArgs bbstestrunner.Args
var bbsBinPath string
var bbsURL *url.URL
var bbsRunner *ginkgomon.Runner
var bbsProcess ifrit.Process
var bbsClient bbs.Client
var auctioneerServer *ghttp.Server

func TestRep(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Rep Integration Suite")
}

var _ = SynchronizedBeforeSuite(func() []byte {
	bbsConfig, err := gexec.Build("github.com/cloudfoundry-incubator/bbs/cmd/bbs", "-race")
	Expect(err).NotTo(HaveOccurred())

	representative, err := gexec.Build("github.com/cloudfoundry-incubator/rep/cmd/rep", "-race")
	Expect(err).NotTo(HaveOccurred())

	return []byte(strings.Join([]string{representative, bbsConfig}, ","))
}, func(pathsByte []byte) {
	path := string(pathsByte)
	representativePath = strings.Split(path, ",")[0]
	bbsBinPath = strings.Split(path, ",")[1]

	cellID = "the-rep-id-" + strconv.Itoa(GinkgoParallelNode())

	etcdPort = 4001 + GinkgoParallelNode()
	serverPort = 1800 + GinkgoParallelNode()

	etcdRunner = etcdstorerunner.NewETCDClusterRunner(etcdPort, 1, nil)

	consulRunner = consulrunner.NewClusterRunner(
		9001+config.GinkgoConfig.ParallelNode*consulrunner.PortOffsetLength,
		1,
		"http",
	)

	etcdRunner.Start()
	consulRunner.Start()

	bbsAddress := fmt.Sprintf("127.0.0.1:%d", 13000+GinkgoParallelNode())

	bbsURL = &url.URL{
		Scheme: "http",
		Host:   bbsAddress,
	}

	bbsClient = bbs.NewClient(bbsURL.String())

	auctioneerServer = ghttp.NewServer()
	auctioneerServer.AppendHandlers(ghttp.RespondWith(http.StatusAccepted, nil))

	etcdUrl := fmt.Sprintf("http://127.0.0.1:%d", etcdPort)
	bbsArgs = bbstestrunner.Args{
		Address:           bbsAddress,
		AuctioneerAddress: auctioneerServer.URL(),
		EtcdCluster:       etcdUrl,
		ConsulCluster:     consulRunner.ConsulCluster(),
	}
	bbsRunner = bbstestrunner.New(bbsBinPath, bbsArgs)
	bbsProcess = ginkgomon.Invoke(bbsRunner)
})

var _ = BeforeEach(func() {
	consulRunner.WaitUntilReady()
	consulRunner.Reset()

	etcdRunner.Reset()

	consulSession = consulRunner.NewSession("a-session")
})

var _ = SynchronizedAfterSuite(func() {
	if bbsProcess != nil {
		ginkgomon.Kill(bbsProcess)
	}
	if etcdRunner != nil {
		etcdRunner.KillWithFire()
	}
	if consulRunner != nil {
		consulRunner.Stop()
	}
	if runner != nil {
		runner.KillWithFire()
	}
	if auctioneerServer != nil {
		auctioneerServer.Close()
	}
}, func() {
	gexec.CleanupBuildArtifacts()
})
