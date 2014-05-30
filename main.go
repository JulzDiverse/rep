package main

import (
	"flag"
	"log"
	"net/http"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/cloudfoundry-incubator/auction/auctionrep"
	"github.com/cloudfoundry-incubator/auction/communication/nats/repnatsserver"
	"github.com/cloudfoundry-incubator/executor/client"
	"github.com/cloudfoundry-incubator/rep/api"
	"github.com/cloudfoundry-incubator/rep/api/lrprunning"
	"github.com/cloudfoundry-incubator/rep/api/taskcomplete"
	"github.com/cloudfoundry-incubator/rep/auction_delegate"
	"github.com/cloudfoundry-incubator/rep/lrp_stopper"
	"github.com/cloudfoundry-incubator/rep/maintain"
	"github.com/cloudfoundry-incubator/rep/routes"
	"github.com/cloudfoundry-incubator/rep/task_scheduler"
	Bbs "github.com/cloudfoundry-incubator/runtime-schema/bbs"
	"github.com/cloudfoundry-incubator/runtime-schema/models"
	steno "github.com/cloudfoundry/gosteno"
	"github.com/cloudfoundry/gunk/timeprovider"
	"github.com/cloudfoundry/storeadapter/etcdstoreadapter"
	"github.com/cloudfoundry/storeadapter/workerpool"
	"github.com/cloudfoundry/yagnats"
	"github.com/nu7hatch/gouuid"
	"github.com/tedsuo/ifrit"
	"github.com/tedsuo/ifrit/grouper"
	"github.com/tedsuo/ifrit/http_server"
	"github.com/tedsuo/ifrit/sigmon"
	"github.com/tedsuo/router"
)

var etcdCluster = flag.String(
	"etcdCluster",
	"http://127.0.0.1:4001",
	"comma-separated list of etcd addresses (http://ip:port)",
)

var natsAddresses = flag.String(
	"natsAddresses",
	"127.0.0.1:4222",
	"comma-separated list of NATS addresses (ip:port)",
)

var natsUsername = flag.String(
	"natsUsername",
	"nats",
	"Username to connect to nats",
)

var natsPassword = flag.String(
	"natsPassword",
	"nats",
	"Password for nats user",
)

var logLevel = flag.String(
	"logLevel",
	"info",
	"the logging level (none, fatal, error, warn, info, debug, debug1, debug2, all)",
)

var syslogName = flag.String(
	"syslogName",
	"",
	"syslog name",
)

var heartbeatInterval = flag.Duration(
	"heartbeatInterval",
	60*time.Second,
	"the interval, in seconds, between heartbeats for maintaining presence",
)

var executorURL = flag.String(
	"executorURL",
	"http://127.0.0.1:1700",
	"location of executor to represent",
)

var lrpHost = flag.String(
	"lrpHost",
	"",
	"address to route traffic to for LRP access",
)

var listenAddr = flag.String(
	"listenAddr",
	"0.0.0.0:20515",
	"host:port to listen on for job completion",
)

var stack = flag.String(
	"stack",
	"",
	"the rep stack - must be specified",
)

func main() {
	flag.Parse()
	if *stack == "" {
		log.Fatalf("-stack must be specified")
	}

	if *lrpHost == "" {
		log.Fatalf("-lrpHost must be specified")
	}

	repID := generateRepID()
	logger := initializeLogger()
	bbs := initializeRepBBS(logger)
	executorClient := client.New(http.DefaultClient, *executorURL)

	group := grouper.EnvokeGroup(grouper.RunGroup{
		"maintainer":     initializeMaintainer(repID, bbs, logger),
		"task-rep":       initializeTaskRep(bbs, logger, executorClient),
		"lrp-stopper":    initializeLRPStopper(bbs, executorClient, logger),
		"api-server":     initializeAPIServer(bbs, logger, executorClient),
		"auction-server": initializeAuctionNatsServer(repID, bbs, executorClient, logger),
	})
	monitor := ifrit.Envoke(sigmon.New(group))

	logger.Info("representative started")

	workerExited := group.Exits()
	monitorExited := monitor.Wait()

	for {
		select {
		case member := <-workerExited:
			logger.Infof("%s exited", member.Name)
			monitor.Signal(syscall.SIGTERM)
		case err := <-monitorExited:
			if err != nil {
				logger.Fatalf("rep existed with error: %s", err)
			}
			os.Exit(0)
		}
	}
}

func initializeLogger() *steno.Logger {
	l, err := steno.GetLogLevel(*logLevel)
	if err != nil {
		log.Fatalf("Invalid loglevel: %s\n", *logLevel)
	}

	stenoConfig := steno.Config{
		Level: l,
		Sinks: []steno.Sink{steno.NewIOSink(os.Stdout)},
	}

	if *syslogName != "" {
		stenoConfig.Sinks = append(stenoConfig.Sinks, steno.NewSyslogSink(*syslogName))
	}

	steno.Init(&stenoConfig)
	return steno.NewLogger("rep")
}

func initializeRepBBS(logger *steno.Logger) Bbs.RepBBS {
	etcdAdapter := etcdstoreadapter.NewETCDStoreAdapter(
		strings.Split(*etcdCluster, ","),
		workerpool.NewWorkerPool(10),
	)

	bbs := Bbs.NewRepBBS(etcdAdapter, timeprovider.NewTimeProvider(), logger)
	err := etcdAdapter.Connect()
	if err != nil {
		logger.Errord(map[string]interface{}{
			"error": err,
		}, "rep.etcd-connect.failed")
		os.Exit(1)
	}
	return bbs
}

func initializeTaskRep(bbs Bbs.RepBBS, logger *steno.Logger, executorClient client.Client) *task_scheduler.TaskScheduler {
	callbackGenerator := router.NewRequestGenerator(
		"http://"+*listenAddr,
		routes.Routes,
	)

	return task_scheduler.New(callbackGenerator, bbs, logger, *stack, executorClient)
}

func generateRepID() string {
	uuid, err := uuid.NewV4()
	if err != nil {
		panic("Failed to generate a random guid....:" + err.Error())
	}
	return uuid.String()
}

func initializeLRPStopper(bbs Bbs.RepBBS, executorClient client.Client, logger *steno.Logger) ifrit.Runner {
	return lrp_stopper.New(bbs, executorClient, logger)
}

func initializeAPIServer(bbs Bbs.RepBBS, logger *steno.Logger, executorClient client.Client) ifrit.Runner {
	taskCompleteHandler := taskcomplete.NewHandler(bbs, logger)
	lrpRunningHandler := lrprunning.NewHandler(bbs, executorClient, *lrpHost, logger)

	apiHandler, err := api.NewServer(taskCompleteHandler, lrpRunningHandler)
	if err != nil {
		panic("failed to initialize api server: " + err.Error())
	}
	return http_server.New(*listenAddr, apiHandler)
}

func initializeMaintainer(repID string, bbs Bbs.RepBBS, logger *steno.Logger) *maintain.Maintainer {
	repPresence := models.RepPresence{
		RepID: repID,
		Stack: *stack,
	}

	return maintain.New(repPresence, bbs, logger, *heartbeatInterval)
}

func initializeNatsClient(logger *steno.Logger) yagnats.NATSClient {
	natsClient := yagnats.NewClient()

	natsMembers := []yagnats.ConnectionProvider{}
	for _, addr := range strings.Split(*natsAddresses, ",") {
		natsMembers = append(
			natsMembers,
			&yagnats.ConnectionInfo{addr, *natsUsername, *natsPassword},
		)
	}

	err := natsClient.Connect(&yagnats.ConnectionCluster{
		Members: natsMembers,
	})

	if err != nil {
		logger.Fatalf("Error connecting to NATS: %s\n", err)
	}

	return natsClient
}

func initializeAuctionNatsServer(repID string, bbs Bbs.RepBBS, executorClient client.Client, logger *steno.Logger) *repnatsserver.RepNatsServer {
	auctionDelegate := auction_delegate.New(bbs, executorClient, logger)
	auctionRep := auctionrep.New(repID, auctionDelegate)
	natsClient := initializeNatsClient(logger)
	return repnatsserver.New(natsClient, auctionRep)
}
