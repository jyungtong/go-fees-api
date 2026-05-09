package bill

import (
	"context"
	"fmt"

	"encore.dev/storage/sqldb"
	"go.temporal.io/sdk/client"
)

const taskQueue = "bill-task-queue"

var db = sqldb.NewDatabase("fees_db", sqldb.DatabaseConfig{
	Migrations: "./migrations",
})

//encore:service
type Service struct {
	temporalClient client.Client
}

func initService() (*Service, error) {
	c, err := client.Dial(client.Options{
		HostPort: "localhost:7233",
	})
	if err != nil {
		return nil, fmt.Errorf("create temporal client: %w", err)
	}

	return &Service{temporalClient: c}, nil
}

func (s *Service) Shutdown(force context.Context) {
	s.temporalClient.Close()
}
