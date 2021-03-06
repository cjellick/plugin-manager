package events

import (
	"github.com/fsouza/go-dockerclient"
	"github.com/rancher/plugin-manager/binexec"
	"github.com/rancher/plugin-manager/network"
)

const (
	simulatedEvent = "-simulated-"
)

func Watch(poolSize int, nm *network.Manager, bw *binexec.Watcher) error {
	dep := &DockerEventsProcessor{
		poolSize: poolSize,
		nm:       nm,
		bw:       bw,
	}
	return dep.Process()
}

type DockerEventsProcessor struct {
	poolSize int
	nm       *network.Manager
	bw       *binexec.Watcher
}

func (de *DockerEventsProcessor) Process() error {
	dockerClient, err := NewDockerClient()
	if err != nil {
		return err
	}

	nmHandler := &NetworkManagerHandler{de.nm}
	handlers := map[string][]Handler{
		"start": []Handler{
			de.bw,
			&StartHandler{dockerClient},
			nmHandler,
		},
		"die": []Handler{
			nmHandler,
		},
	}

	router, err := NewEventRouter(de.poolSize, de.poolSize, dockerClient, handlers)
	if err != nil {
		return err
	}
	router.Start()

	containers, err := dockerClient.ListContainers(docker.ListContainersOptions{
		All: true,
	})
	if err != nil {
		return err
	}

	for _, c := range containers {
		event := &docker.APIEvents{
			ID:     c.ID,
			Status: "start",
			From:   simulatedEvent,
		}
		router.listener <- event
	}

	return nil
}
