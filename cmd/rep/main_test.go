package main_test

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/cloudfoundry-incubator/auction/auctiontypes"
	"github.com/cloudfoundry-incubator/bbs"
	"github.com/cloudfoundry-incubator/bbs/models"
	"github.com/cloudfoundry-incubator/bbs/models/test/model_helpers"
	"github.com/cloudfoundry-incubator/executor"
	"github.com/cloudfoundry-incubator/executor/depot/gardenstore"
	"github.com/cloudfoundry-incubator/garden"
	"github.com/cloudfoundry-incubator/rep"
	"github.com/cloudfoundry-incubator/rep/cmd/rep/testrunner"
	oldmodels "github.com/cloudfoundry-incubator/runtime-schema/models"
	"github.com/cloudfoundry/storeadapter"
	"github.com/pivotal-golang/clock"
	"github.com/pivotal-golang/lager/lagertest"

	legacybbs "github.com/cloudfoundry-incubator/runtime-schema/bbs"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gbytes"
	. "github.com/onsi/gomega/gexec"
	"github.com/onsi/gomega/ghttp"
)

var runner *testrunner.Runner
var etcdAdapter storeadapter.StoreAdapter

var _ = Describe("The Rep", func() {
	var (
		fakeGarden        *ghttp.Server
		legacyBBS         *legacybbs.BBS
		pollingInterval   time.Duration
		evacuationTimeout time.Duration
		rootFSName        string
		rootFSPath        string
		logger            *lagertest.TestLogger

		flushEvents chan struct{}
	)

	var getActualLRPGroups = func() []*models.ActualLRPGroup {
		actualLRPGroups, err := bbsClient.ActualLRPGroups(models.ActualLRPFilter{})
		Expect(err).NotTo(HaveOccurred())
		return actualLRPGroups
	}

	BeforeEach(func() {
		Eventually(getActualLRPGroups, 5*pollingInterval).Should(BeEmpty())
		flushEvents = make(chan struct{})
		fakeGarden = ghttp.NewUnstartedServer()
		// these tests only look for the start of a sequence of requests
		fakeGarden.AllowUnhandledRequests = false
		fakeGarden.RouteToHandler("GET", "/ping", ghttp.RespondWithJSONEncoded(http.StatusOK, struct{}{}))
		fakeGarden.RouteToHandler("GET", "/containers", ghttp.RespondWithJSONEncoded(http.StatusOK, struct{}{}))
		fakeGarden.RouteToHandler("GET", "/capacity", ghttp.RespondWithJSONEncoded(http.StatusOK,
			garden.Capacity{MemoryInBytes: 1024 * 1024 * 1024, DiskInBytes: 2048 * 1024 * 1024, MaxContainers: 4}))
		fakeGarden.RouteToHandler("GET", "/containers/bulk_info", ghttp.RespondWithJSONEncoded(http.StatusOK, struct{}{}))
		etcdAdapter = etcdRunner.Adapter(nil)

		logger = lagertest.NewTestLogger("test")
		legacyBBS = legacybbs.NewBBS(etcdAdapter, consulSession, clock.NewClock(), logger)

		pollingInterval = 50 * time.Millisecond
		evacuationTimeout = 200 * time.Millisecond

		rootFSName = "the-rootfs"
		rootFSPath = "/path/to/rootfs"
		rootFSArg := fmt.Sprintf("%s:%s", rootFSName, rootFSPath)

		runner = testrunner.New(
			representativePath,
			testrunner.Config{
				PreloadedRootFSes: []string{rootFSArg},
				RootFSProviders:   []string{"docker"},
				CellID:            cellID,
				BBSAddress:        bbsURL.String(),
				EtcdCluster:       fmt.Sprintf("http://127.0.0.1:%d", etcdPort),
				ServerPort:        serverPort,
				GardenAddr:        fakeGarden.HTTPTestServer.Listener.Addr().String(),
				LogLevel:          "info",
				ConsulCluster:     consulRunner.ConsulCluster(),
				PollingInterval:   pollingInterval,
				EvacuationTimeout: evacuationTimeout,
			},
		)
	})

	JustBeforeEach(func() {
		runner.Start()
	})

	AfterEach(func(done Done) {
		close(flushEvents)
		etcdAdapter.Disconnect()
		runner.KillWithFire()
		fakeGarden.Close()
		close(done)
	})

	Context("when Garden is available", func() {
		BeforeEach(func() {
			fakeGarden.Start()
		})

		Describe("when an interrupt signal is sent to the representative", func() {
			JustBeforeEach(func() {
				runner.Stop()
			})

			It("should die", func() {
				Eventually(runner.Session.ExitCode).Should(Equal(0))
			})
		})

		Context("when etcd is down", func() {
			BeforeEach(func() {
				etcdRunner.Stop()
			})

			AfterEach(func() {
				etcdRunner.Start()
			})

			It("starts", func() {
				Consistently(runner.Session).ShouldNot(Exit())
			})
		})

		Context("when starting", func() {
			var deleteChan chan struct{}
			BeforeEach(func() {
				fakeGarden.RouteToHandler("GET", "/containers",
					ghttp.RespondWithJSONEncoded(http.StatusOK, map[string][]string{"handles": []string{"cnr1", "cnr2"}}),
				)

				deleteChan = make(chan struct{}, 2)
				fakeGarden.AppendHandlers(
					ghttp.CombineHandlers(ghttp.VerifyRequest("DELETE", "/containers/cnr1"),
						func(http.ResponseWriter, *http.Request) {
							deleteChan <- struct{}{}
						},
						ghttp.RespondWithJSONEncoded(http.StatusOK, &struct{}{})),
					ghttp.CombineHandlers(ghttp.VerifyRequest("DELETE", "/containers/cnr2"),
						func(http.ResponseWriter, *http.Request) {
							deleteChan <- struct{}{}
						},
						ghttp.RespondWithJSONEncoded(http.StatusOK, &struct{}{})),
				)
			})

			It("destroys any existing containers", func() {
				Eventually(deleteChan).Should(Receive())
				Eventually(deleteChan).Should(Receive())
			})
		})

		Describe("maintaining presence", func() {
			var cellPresence oldmodels.CellPresence

			JustBeforeEach(func() {
				Eventually(legacyBBS.Cells).Should(HaveLen(1))
				cells, err := legacyBBS.Cells()
				Expect(err).NotTo(HaveOccurred())
				cellPresence = cells[0]
			})

			It("should maintain presence", func() {
				Expect(cellPresence.CellID).To(Equal(cellID))
				expectedRootFSProviders := map[string][]string{
					"docker":    []string{},
					"preloaded": []string{"the-rootfs"},
				}
				Expect(cellPresence.RootFSProviders).To(Equal(expectedRootFSProviders))
			})

			It("should have no session health checks", func() {
				sessions, _, err := consulRunner.NewClient().Session().List(nil)
				Expect(err).NotTo(HaveOccurred())
				Expect(sessions).To(HaveLen(1))
				Expect(sessions[0].Checks).To(BeEmpty())
			})

			Context("when the presence fails to be maintained", func() {
				It("should not exit, but keep trying to maintain presence at the same ID", func() {
					consulRunner.Reset()

					Eventually(legacyBBS.Cells, 5).Should(HaveLen(1))
					cells, err := legacyBBS.Cells()
					Expect(err).NotTo(HaveOccurred())
					Expect(cells[0]).To(Equal(cellPresence))

					Expect(runner.Session).NotTo(Exit())
				})
			})
		})

		Context("acting as an auction representative", func() {
			var client *rep.Client

			JustBeforeEach(func() {
				Eventually(legacyBBS.Cells).Should(HaveLen(1))
				cells, err := legacyBBS.Cells()
				Expect(err).NotTo(HaveOccurred())

				client = rep.NewClient(http.DefaultClient, cells[0].CellID, cells[0].RepAddress, lagertest.NewTestLogger("auction-client"))
			})

			Context("Capacity with a container", func() {
				BeforeEach(func() {
					fakeGarden.RouteToHandler("GET", "/containers",
						ghttp.RespondWithJSONEncoded(http.StatusOK, map[string][]string{"handles": []string{"handle-guid"}}),
					)
					fakeGarden.RouteToHandler("GET", "/containers/bulk_info",
						ghttp.RespondWithJSONEncoded(http.StatusOK,
							map[string]garden.ContainerInfoEntry{
								"handle-guid": garden.ContainerInfoEntry{Info: garden.ContainerInfo{
									Properties: map[string]string{
										gardenstore.ContainerStateProperty:    string(executor.StateRunning),
										gardenstore.ContainerMemoryMBProperty: "512", gardenstore.ContainerDiskMBProperty: "1024"},
								}},
							},
						),
					)

					fakeGarden.RouteToHandler("DELETE", "/containers/handle-guid", ghttp.RespondWithJSONEncoded(http.StatusOK, &struct{}{}))

					// In case the bulker loop is executed
					fakeGarden.RouteToHandler("GET", "/containers/handle-guid/info", ghttp.RespondWithJSONEncoded(http.StatusInternalServerError, garden.ContainerInfo{}))
				})

				It("returns total capacity", func() {
					state, err := client.State()
					Expect(err).NotTo(HaveOccurred())
					Expect(state.TotalResources).To(Equal(auctiontypes.Resources{
						MemoryMB:   1024,
						DiskMB:     2048,
						Containers: 4,
					}))
				})

				It("returns available capacity", func() {
					Eventually(func() auctiontypes.Resources {
						state, err := client.State()
						Expect(err).NotTo(HaveOccurred())
						return state.AvailableResources
					}).Should(Equal(auctiontypes.Resources{
						MemoryMB:   512,
						DiskMB:     1024,
						Containers: 3,
					}))
				})

				Context("when the container is removed", func() {
					It("returns available capacity == total capacity", func() {
						fakeGarden.RouteToHandler("GET", "/containers", ghttp.RespondWithJSONEncoded(http.StatusOK, struct{}{}))
						fakeGarden.RouteToHandler("GET", "/containers/bulk_info", ghttp.RespondWithJSONEncoded(http.StatusOK, struct{}{}))

						Eventually(func() auctiontypes.Resources {
							state, err := client.State()
							Expect(err).NotTo(HaveOccurred())
							return state.AvailableResources
						}).Should(Equal(auctiontypes.Resources{
							MemoryMB:   1024,
							DiskMB:     2048,
							Containers: 4,
						}))
					})
				})
			})

			Describe("running tasks", func() {
				var task *models.Task
				var containersCalled chan struct{}

				JustBeforeEach(func() {
					containersCalled = make(chan struct{})
					fakeGarden.RouteToHandler("POST", "/containers", ghttp.CombineHandlers(
						ghttp.RespondWithJSONEncoded(http.StatusOK, map[string]string{"handle": "handle-guid"}),
						func(w http.ResponseWriter, r *http.Request) {
							close(containersCalled)
						},
					))

					fakeGarden.RouteToHandler("PUT", "/containers/handle-guid/limits/memory", ghttp.RespondWithJSONEncoded(http.StatusOK, garden.MemoryLimits{}))
					fakeGarden.RouteToHandler("PUT", "/containers/handle-guid/limits/disk", ghttp.RespondWithJSONEncoded(http.StatusOK, garden.DiskLimits{}))
					fakeGarden.RouteToHandler("PUT", "/containers/handle-guid/limits/cpu", ghttp.RespondWithJSONEncoded(http.StatusOK, garden.CPULimits{}))
					fakeGarden.RouteToHandler("GET", "/containers/handle-guid/info", ghttp.RespondWithJSONEncoded(http.StatusOK, garden.ContainerInfo{}))

					task = &models.Task{
						TaskGuid: "the-task-guid",
						Domain:   "the-domain",
						TaskDefinition: &models.TaskDefinition{
							MemoryMb: 2,
							DiskMb:   2,
							RootFs:   "the:rootfs",
							Action: models.WrapAction(&models.RunAction{
								User: "me",
								Path: "date",
							}),
						},
					}

					err := bbsClient.DesireTask(task.TaskGuid, task.Domain, task.TaskDefinition)
					Expect(err).NotTo(HaveOccurred())
				})

				It("makes a request to executor to allocate the container", func() {
					Expect(getTasksByState(bbsClient, models.Task_Pending)).To(HaveLen(1))
					Expect(getTasksByState(bbsClient, models.Task_Running)).To(BeEmpty())

					works := auctiontypes.Work{
						Tasks: []*models.Task{task},
					}

					failedWorks, err := client.Perform(works)
					Expect(err).NotTo(HaveOccurred())
					Expect(failedWorks.Tasks).To(BeEmpty())

					Eventually(containersCalled).Should(BeClosed())
				})
			})
		})

		Describe("polling the BBS for tasks to reap", func() {
			var task *models.Task

			JustBeforeEach(func() {
				task = model_helpers.NewValidTask("task-guid")
				err := bbsClient.DesireTask(task.TaskGuid, task.Domain, task.TaskDefinition)
				Expect(err).NotTo(HaveOccurred())

				_, err = bbsClient.StartTask(task.TaskGuid, cellID)
				Expect(err).NotTo(HaveOccurred())
			})

			It("eventually marks tasks with no corresponding container as failed", func() {
				Eventually(func() []*models.Task {
					return getTasksByState(bbsClient, models.Task_Completed)
				}, 5*pollingInterval).Should(HaveLen(1))

				completedTasks := getTasksByState(bbsClient, models.Task_Completed)

				Expect(completedTasks[0].TaskGuid).To(Equal(task.TaskGuid))
				Expect(completedTasks[0].Failed).To(BeTrue())
			})
		})

		Describe("polling the BBS for actual LRPs to reap", func() {
			JustBeforeEach(func() {
				desiredLRP := &models.DesiredLRP{
					ProcessGuid: "process-guid",
					RootFs:      "some:rootfs",
					Domain:      "some-domain",
					Instances:   1,
					Action: models.WrapAction(&models.RunAction{
						User: "me",
						Path: "the-path",
						Args: []string{},
					}),
				}
				index := 0

				err := bbsClient.DesireLRP(desiredLRP)
				Expect(err).NotTo(HaveOccurred())

				instanceKey := models.NewActualLRPInstanceKey("some-instance-guid", cellID)
				err = bbsClient.ClaimActualLRP(desiredLRP.ProcessGuid, index, &instanceKey)
				Expect(err).NotTo(HaveOccurred())
			})

			It("eventually reaps actual LRPs with no corresponding container", func() {
				Eventually(getActualLRPGroups, 5*pollingInterval).Should(BeEmpty())
			})
		})

		Describe("when a StopLRPInstance request comes in", func() {
			const processGuid = "process-guid"
			const instanceGuid = "some-instance-guid"
			var runningLRP *models.ActualLRP
			var containerGuid = rep.LRPContainerGuid(processGuid, instanceGuid)
			var expectedDestroyRoute = "/containers/" + containerGuid

			JustBeforeEach(func() {
				fakeGarden.RouteToHandler("DELETE", expectedDestroyRoute,
					ghttp.RespondWithJSONEncoded(http.StatusOK, &struct{}{}),
				)

				// ensure the container remains after being stopped
				fakeGarden.RouteToHandler("GET", "/containers",
					ghttp.RespondWithJSONEncoded(http.StatusOK, map[string][]string{
						"handles": []string{"my-handle"},
					}),
				)

				containerInfo := garden.ContainerInfo{
					Properties: map[string]string{
						gardenstore.ContainerStateProperty:    string(executor.StateRunning),
						gardenstore.ContainerMemoryMBProperty: "512", gardenstore.ContainerDiskMBProperty: "1024"},
				}
				fakeGarden.RouteToHandler("GET", "/containers/bulk_info",
					ghttp.RespondWithJSONEncoded(http.StatusOK,
						map[string]garden.ContainerInfoEntry{
							"my-handle": garden.ContainerInfoEntry{Info: containerInfo},
						},
					),
				)

				fakeGarden.RouteToHandler("GET", "/containers/my-handle/info", ghttp.RespondWithJSONEncoded(http.StatusOK, containerInfo))

				Eventually(legacyBBS.Cells).Should(HaveLen(1))

				lrpKey := models.NewActualLRPKey(processGuid, 1, "domain")
				instanceKey := models.NewActualLRPInstanceKey(instanceGuid, cellID)
				netInfo := models.NewActualLRPNetInfo("bogus-ip")

				err := bbsClient.StartActualLRP(&lrpKey, &instanceKey, &netInfo)
				Expect(err).NotTo(HaveOccurred())

				actualLRPGroup, err := bbsClient.ActualLRPGroupByProcessGuidAndIndex(lrpKey.ProcessGuid, int(lrpKey.Index))
				Expect(err).NotTo(HaveOccurred())
				runningLRP = actualLRPGroup.GetInstance()
			})

			It("should destroy the container", func() {
				Eventually(getActualLRPGroups).Should(HaveLen(1))
				bbsClient.RetireActualLRP(&runningLRP.ActualLRPKey)

				findDestroyRequest := func() bool {
					for _, req := range fakeGarden.ReceivedRequests() {
						if req.URL.Path == expectedDestroyRoute {
							return true
						}
					}
					return false
				}

				Eventually(findDestroyRequest).Should(BeTrue())
			})
		})

		Describe("cancelling tasks", func() {
			const taskGuid = "some-task-guid"
			const expectedDeleteRoute = "/containers/" + taskGuid

			var deletedContainer chan struct{}

			JustBeforeEach(func() {
				Eventually(legacyBBS.Cells).Should(HaveLen(1))

				deletedContainer = make(chan struct{})

				fakeGarden.RouteToHandler("DELETE", expectedDeleteRoute,
					ghttp.CombineHandlers(
						func(http.ResponseWriter, *http.Request) {
							close(deletedContainer)
						},
						ghttp.RespondWithJSONEncoded(http.StatusOK, &struct{}{}),
					),
				)

				task := model_helpers.NewValidTask(taskGuid)

				err := bbsClient.DesireTask(task.TaskGuid, task.Domain, task.TaskDefinition)
				Expect(err).NotTo(HaveOccurred())

				started, err := bbsClient.StartTask(taskGuid, cellID)
				Expect(err).NotTo(HaveOccurred())
				Expect(started).To(BeTrue())
			})

			It("deletes the container", func() {
				err := bbsClient.CancelTask(taskGuid)
				Expect(err).NotTo(HaveOccurred())

				Eventually(deletedContainer).Should(BeClosed())

				Consistently(func() ([]*models.Task, error) {
					return bbsClient.Tasks()
				}).Should(HaveLen(1))
			})
		})

		Describe("Evacuation", func() {
			Context("when it has running LRP containers", func() {
				var (
					processGuid  string
					index        int32
					domain       string
					instanceGuid string
					address      string

					lrpKey          models.ActualLRPKey
					lrpContainerKey models.ActualLRPInstanceKey
					lrpNetInfo      models.ActualLRPNetInfo
				)

				JustBeforeEach(func() {
					processGuid = "some-process-guid"
					index = 2
					domain = "some-domain"

					instanceGuid = "some-instance-guid"
					address = "some-external-ip"

					containerGuid := rep.LRPContainerGuid(processGuid, instanceGuid)

					containerInfo := garden.ContainerInfo{
						ExternalIP: "localhost",
						Properties: map[string]string{
							gardenstore.ContainerStateProperty:    string(executor.StateRunning),
							gardenstore.ContainerMemoryMBProperty: "512", gardenstore.ContainerDiskMBProperty: "1024",
							"tag:" + rep.LifecycleTag:    rep.LRPLifecycle,
							"tag:" + rep.DomainTag:       "domain",
							"tag:" + rep.ProcessGuidTag:  processGuid,
							"tag:" + rep.InstanceGuidTag: instanceGuid,
							"tag:" + rep.ProcessIndexTag: strconv.Itoa(int(index)),
						}}
					fakeGarden.RouteToHandler("GET", "/containers",
						ghttp.RespondWithJSONEncoded(http.StatusOK, map[string][]string{"handles": []string{containerGuid}}),
					)
					fakeGarden.RouteToHandler("GET", "/containers/bulk_info",
						ghttp.RespondWithJSONEncoded(http.StatusOK,
							map[string]garden.ContainerInfoEntry{
								containerGuid: garden.ContainerInfoEntry{Info: containerInfo},
							},
						),
					)
					fakeGarden.RouteToHandler("DELETE", "/containers/"+containerGuid, ghttp.RespondWithJSONEncoded(http.StatusOK, &struct{}{}))

					fakeGarden.RouteToHandler("GET", "/containers/"+containerGuid+"/info", ghttp.RespondWithJSONEncoded(http.StatusOK, containerInfo))

					lrpKey = models.NewActualLRPKey(processGuid, index, domain)
					lrpContainerKey = models.NewActualLRPInstanceKey(instanceGuid, cellID)
					lrpNetInfo = models.NewActualLRPNetInfo(address, models.NewPortMapping(2589, 1470))

					err := bbsClient.StartActualLRP(&lrpKey, &lrpContainerKey, &lrpNetInfo)
					Expect(err).NotTo(HaveOccurred())

					resp, err := http.Post(fmt.Sprintf("http://0.0.0.0:%d/evacuate", serverPort), "text/html", nil)
					Expect(err).NotTo(HaveOccurred())
					resp.Body.Close()
					Expect(resp.StatusCode).To(Equal(http.StatusAccepted))
				})

				It("evacuates them", func() {
					getEvacuatingLRP := func() *models.ActualLRP {
						actualLRPGroup, err := bbsClient.ActualLRPGroupByProcessGuidAndIndex(processGuid, int(index))
						Expect(err).NotTo(HaveOccurred())

						return actualLRPGroup.Evacuating
					}

					Eventually(getEvacuatingLRP, 1).ShouldNot(BeNil())
					Expect(getEvacuatingLRP().ProcessGuid).To(Equal(processGuid))
				})

				Context("when exceeding the evacuation timeout", func() {
					It("shuts down gracefully", func() {
						// wait longer than expected to let OS and Go runtime reap process
						Eventually(runner.Session.ExitCode, 2*evacuationTimeout+2*time.Second).Should(Equal(0))
					})
				})

				Context("when signaled to stop", func() {
					JustBeforeEach(func() {
						runner.Stop()
					})

					It("shuts down gracefully", func() {
						Eventually(runner.Session.ExitCode).Should(Equal(0))
					})
				})
			})
		})

		Describe("when a Ping request comes in", func() {
			It("responds with 200 OK", func() {
				resp, err := http.Get(fmt.Sprintf("http://0.0.0.0:%d/ping", serverPort))
				Expect(err).NotTo(HaveOccurred())
				Expect(resp.StatusCode).To(Equal(http.StatusOK))
			})
		})
	})

	Context("when Garden is unavailable", func() {
		BeforeEach(func() {
			runner.StartCheck = ""
		})

		It("should not exit and continue waiting for a connection", func() {
			Consistently(runner.Session.Buffer()).ShouldNot(gbytes.Say("started"))
			Consistently(runner.Session).ShouldNot(Exit())
		})

		Context("when Garden starts", func() {
			JustBeforeEach(func() {
				fakeGarden.Start()
				// these tests only look for the start of a sequence of requests
				fakeGarden.AllowUnhandledRequests = false
			})

			It("should connect", func() {
				Eventually(runner.Session.Buffer(), 5*time.Second).Should(gbytes.Say("started"))
			})
		})
	})
})

func getTasksByState(client bbs.Client, state models.Task_State) []*models.Task {
	tasks, err := client.Tasks()
	Expect(err).NotTo(HaveOccurred())

	filteredTasks := make([]*models.Task, 0)
	for _, task := range tasks {
		if task.State == state {
			filteredTasks = append(filteredTasks, task)
		}
	}
	return filteredTasks
}
