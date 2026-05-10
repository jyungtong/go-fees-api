package bill

import (
	"context"
	"fmt"
	"os"

	"encore.dev/storage/sqldb"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
)

const taskQueue = "bill-task-queue"

var db = sqldb.NewDatabase("fees_db", sqldb.DatabaseConfig{
	Migrations: "./migrations",
})

//encore:service
type Service struct {
	temporalClient client.Client
	temporalWorker worker.Worker
}

func initService() (*Service, error) {
	hostPort := os.Getenv("TEMPORAL_HOST_PORT")
	if hostPort == "" {
		hostPort = "localhost:7233"
	}

	c, err := client.Dial(client.Options{
		HostPort: hostPort,
	})
	if err != nil {
		return nil, fmt.Errorf("create temporal client: %w", err)
	}

	w := worker.New(c, taskQueue, worker.Options{})
	w.RegisterWorkflow(BillWorkflow)
	w.RegisterActivity(CreateBillActivity)
	w.RegisterActivity(AddLineItemActivity)
	w.RegisterActivity(CloseBillActivity)

	err = w.Start()
	if err != nil {
		return nil, fmt.Errorf("start temporal worker: %w", err)
	}

	return &Service{temporalClient: c, temporalWorker: w}, nil
}

func (s *Service) Shutdown(force context.Context) {
	s.temporalWorker.Stop()
	s.temporalClient.Close()
}
