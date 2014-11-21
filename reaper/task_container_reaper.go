package reaper

import (
	"github.com/cloudfoundry-incubator/executor"
	"github.com/cloudfoundry-incubator/rep"
	"github.com/cloudfoundry-incubator/rep/gatherer"
	"github.com/cloudfoundry-incubator/runtime-schema/models"
	"github.com/pivotal-golang/lager"
)

type TaskContainerReaper struct {
	executorClient executor.Client
	logger         lager.Logger
}

func NewTaskContainerReaper(
	executorClient executor.Client,
	logger lager.Logger,
) *TaskContainerReaper {
	return &TaskContainerReaper{
		executorClient: executorClient,
		logger:         logger.Session("task-container-reaper"),
	}
}

func (r *TaskContainerReaper) Process(snapshot gatherer.Snapshot) {
	r.logger.Info("started")

	containers := snapshot.ListContainers(executor.Tags{
		rep.LifecycleTag: rep.TaskLifecycle,
	})

	for _, container := range containers {
		task := snapshot.GetTask(container.Guid)

		taskExists := task != nil

		if !taskExists || task.State == models.TaskStateCompleted || task.State == models.TaskStateResolving {
			lagerData := lager.Data{
				"task-exists": taskExists,
			}
			if task != nil {
				lagerData["task-guid"] = task.TaskGuid
				lagerData["task-state"] = task.State
			}

			r.logger.Info("deleting-container", lagerData)
			err := r.executorClient.DeleteContainer(container.Guid)
			if err != nil {
				r.logger.Error("failed-to-delete-container", err, lagerData)
			}
		}
	}

	r.logger.Info("stopped")
}
