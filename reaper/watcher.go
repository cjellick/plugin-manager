package reaper

import (
	"context"
	"time"

	"github.com/docker/engine-api/client"
	"github.com/docker/engine-api/types"
	"github.com/jpillora/backoff"
	"github.com/leodotcloud/log"
	"github.com/rancher/go-rancher-metadata/metadata"
)

var (
	uuidLabel        = "io.rancher.container.uuid"
	serviceNameLabel = "io.rancher.stack_service.name"
	metadataService  = "network-services/metadata"
	dnsService       = "network-services/metadata/dns"

	recheckEvery = 5 * time.Minute
)

func Watch(dockerClient *client.Client, c metadata.Client) error {
	w := &watcher{
		dc: dockerClient,
		c:  c,
	}
	go c.OnChange(5, w.onChangeNoError)
	go watchMetadata(dockerClient)
	return nil
}

func watchMetadata(dockerClient *client.Client) {
	b := &backoff.Backoff{
		Min:    1 * time.Second,
		Max:    5 * time.Minute,
		Factor: 1.5,
	}
	for {
		err := CheckMetadata(dockerClient)
		if err != nil {
			log.Errorf("reaper: Failed to check for bad metadata: %v", err)
		}
		time.Sleep(b.Duration())
	}
}

type watcher struct {
	dc *client.Client
	c  metadata.Client
}

func (w *watcher) onChangeNoError(version string) {
	if err := w.onChange(version); err != nil {
		log.Errorf("reaper: Failed to watch for orphan containers: %v", err)
	}
}

func (w *watcher) onChange(version string) error {
	host, err := w.c.GetSelfHost()
	if err != nil {
		return err
	}

	containers, err := w.c.GetContainers()
	if err != nil {
		return err
	}

	for _, container := range containers {
		if container.HostUUID != host.UUID {
			continue
		}
		uuid, ok := container.Labels[uuidLabel]
		if !ok {
			continue
		}

		if container.UUID != uuid {
			w.removeContainer(container)
		}
	}

	return nil
}

func CheckMetadata(dockerClient *client.Client) error {
	containers, err := dockerClient.ContainerList(context.Background(), types.ContainerListOptions{
		All: true,
	})
	if err != nil {
		return err
	}

	metadataIds := []string{}
	dnsIds := []string{}
	for _, container := range containers {
		if container.State != "running" {
			continue
		}

		if container.Labels[uuidLabel] != "" && container.Labels[serviceNameLabel] == metadataService {
			metadataIds = append(metadataIds, container.ID)
		}
		if container.Labels[uuidLabel] != "" && container.Labels[serviceNameLabel] == dnsService {
			dnsIds = append(dnsIds, container.ID)
		}
	}

	toStop := []string{}

	// Lists are ordered newest to older so we pick the last to stop
	if len(metadataIds) > 1 {
		toStop = append(toStop, metadataIds[len(metadataIds)-1])
	}
	if len(dnsIds) > 1 {
		toStop = append(toStop, dnsIds[len(dnsIds)-1])
	}

	for _, id := range toStop {
		log.Infof("reaper:  Stopping duplicate metadata/dns service: %s", id)
		t := time.Duration(0)
		if err := dockerClient.ContainerStop(context.Background(), id, &t); err != nil {
			log.Errorf("reaper:  Failed to stop duplicate metadata/dns service: %s", id)
		}
	}

	return nil
}

func (w *watcher) removeContainer(container metadata.Container) {
	c, err := w.dc.ContainerInspect(context.Background(), container.ExternalId)
	if err != nil {
		log.Errorf("reaper: Inspect failed: %v", err)
		return
	}
	if c.Name == "/rancher-agent" {
		return
	}
	log.Infof("reaper:  Removing unmanaged container %s %s", container.Name, container.ExternalId)
	err = w.dc.ContainerRemove(context.Background(), container.ExternalId, types.ContainerRemoveOptions{
		Force: true,
	})
	if err != nil {
		log.Errorf("reaper: Removed failed: %v", err)
	}
}
