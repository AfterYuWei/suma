package docker

import (
	"context"
	"fmt"
	"sync"

	dockercontainer "github.com/docker/docker/api/types/container"
	domain "github.com/suma/suma/server/internal/container"
)

func (a *Adapter) RuntimeMetadata(ctx context.Context) ([]domain.RuntimeMetadata, error) {
	rows, err := a.client.ContainerList(ctx, dockercontainer.ListOptions{All: true})
	if err != nil {
		return nil, fmt.Errorf("list containers for runtime metadata: %w", err)
	}
	result := make([]domain.RuntimeMetadata, len(rows))
	for index, row := range rows {
		result[index].ID = row.ID
	}
	jobs := make(chan int)
	var workers sync.WaitGroup
	for range min(6, len(rows)) {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for index := range jobs {
				inspect, err := a.client.ContainerInspect(ctx, rows[index].ID)
				if err != nil {
					continue
				}
				result[index] = runtimeMetadata(inspect, rows[index].ID)
			}
		}()
	}
enqueue:
	for index := range rows {
		select {
		case jobs <- index:
		case <-ctx.Done():
			break enqueue
		}
	}
	close(jobs)
	workers.Wait()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return result, nil
}
