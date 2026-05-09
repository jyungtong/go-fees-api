package bill

import (
	"context"

	"go.temporal.io/sdk/client"
)

type HealthResponse struct {
	Status            string `json:"status"`
	TemporalReachable bool   `json:"temporal_reachable"`
}

//encore:api public method=GET path=/health
func (s *Service) Health(ctx context.Context) (*HealthResponse, error) {
	_, err := s.temporalClient.CheckHealth(ctx, &client.CheckHealthRequest{})
	return &HealthResponse{
		Status:            "ok",
		TemporalReachable: err == nil,
	}, nil
}
