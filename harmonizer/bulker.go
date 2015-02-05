package harmonizer

import (
	"os"
	"time"

	"github.com/cloudfoundry-incubator/rep/generator"
	"github.com/cloudfoundry-incubator/runtime-schema/metric"
	"github.com/pivotal-golang/clock"
	"github.com/pivotal-golang/lager"
	"github.com/pivotal-golang/operationq"
)

const repBulkSyncDuration = metric.Duration("RepBulkSyncDuration")

type Bulker struct {
	logger lager.Logger

	pollInterval time.Duration
	clock        clock.Clock
	generator    generator.Generator
	queue        operationq.Queue
}

func NewBulker(
	logger lager.Logger,
	pollInterval time.Duration,
	clock clock.Clock,
	generator generator.Generator,
	queue operationq.Queue,
) *Bulker {
	return &Bulker{
		logger: logger,

		pollInterval: pollInterval,
		clock:        clock,
		generator:    generator,
		queue:        queue,
	}
}

func (b *Bulker) Run(signals <-chan os.Signal, ready chan<- struct{}) error {
	close(ready)

	logger := b.logger.Session("bulker")

	logger.Info("starting", lager.Data{"interval": b.pollInterval.String()})
	defer logger.Info("completed")

	ticker := b.clock.NewTicker(b.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C():
			b.sync(logger.Session("sync"))

		case signal := <-signals:
			logger.Info("received-signal", lager.Data{"signal": signal.String()})
			return nil
		}
	}
}

func (b *Bulker) sync(logger lager.Logger) {
	logger.Info("start")
	defer logger.Info("done")

	startTime := b.clock.Now()

	ops, err := b.generator.BatchOperations(logger)

	endTime := b.clock.Now()

	repBulkSyncDuration.Send(endTime.Sub(startTime))

	if err != nil {
		logger.Error("failed-to-generate-operations", err)
		return
	}

	for _, operation := range ops {
		b.queue.Push(operation)
	}
}
