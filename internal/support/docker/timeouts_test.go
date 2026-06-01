package docker_support

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/amir20/dozzle/internal/container"
	container_support "github.com/amir20/dozzle/internal/support/container"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDashboardClientTimeoutCapsLongTimeouts(t *testing.T) {
	old := maxDashboardClientTimeout
	maxDashboardClientTimeout = 25 * time.Millisecond
	defer func() { maxDashboardClientTimeout = old }()

	assert.Equal(t, 10*time.Millisecond, dashboardClientTimeout(10*time.Millisecond))
	assert.Equal(t, 25*time.Millisecond, dashboardClientTimeout(10*time.Second))
	assert.Equal(t, 25*time.Millisecond, dashboardClientTimeout(0))
}

func TestMultiHostServiceListAllContainersDoesNotWaitFullClientTimeout(t *testing.T) {
	old := maxDashboardClientTimeout
	maxDashboardClientTimeout = 25 * time.Millisecond
	defer func() { maxDashboardClientTimeout = old }()

	service := &blockingClientService{host: container.Host{ID: "agent-1", Name: "agent-1", Type: "agent"}}
	manager := &staticClientManager{clients: []container_support.ClientService{service}}
	multiHostService := NewMultiHostService(manager, 10*time.Second)

	start := time.Now()
	containers, errs := multiHostService.ListAllContainers(container.ContainerLabels{})
	elapsed := time.Since(start)

	require.Empty(t, containers)
	require.Len(t, errs, 1)
	assert.Less(t, elapsed, 250*time.Millisecond)
	assert.ErrorIs(t, errs[0], context.DeadlineExceeded)
}

type staticClientManager struct {
	clients []container_support.ClientService
}

func (m *staticClientManager) Find(string) (container_support.ClientService, bool) {
	return nil, false
}

func (m *staticClientManager) List() []container_support.ClientService {
	return m.clients
}

func (m *staticClientManager) RetryAndList() ([]container_support.ClientService, []error) {
	return m.clients, nil
}

func (m *staticClientManager) Subscribe(context.Context, chan<- container.Host) {}

func (m *staticClientManager) Hosts(context.Context) []container.Host {
	return nil
}

func (m *staticClientManager) LocalClients() []container.Client {
	return nil
}

func (m *staticClientManager) LocalClientServices() []container_support.ClientService {
	return nil
}

type blockingClientService struct {
	host container.Host
}

func (c *blockingClientService) FindContainer(context.Context, string, container.ContainerLabels) (container.Container, error) {
	return container.Container{}, nil
}

func (c *blockingClientService) ListContainers(ctx context.Context, _ container.ContainerLabels) ([]container.Container, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func (c *blockingClientService) Host(ctx context.Context) (container.Host, error) {
	select {
	case <-ctx.Done():
		host := c.host
		host.Available = false
		return host, ctx.Err()
	default:
		return c.host, nil
	}
}

func (c *blockingClientService) ContainerAction(context.Context, container.Container, container.ContainerAction) error {
	return nil
}

func (c *blockingClientService) UpdateContainer(context.Context, container.Container, chan<- container.UpdateProgress) (bool, error) {
	return false, nil
}

func (c *blockingClientService) LogsBetweenDates(context.Context, container.Container, time.Time, time.Time, container.StdType) (<-chan *container.LogEvent, error) {
	return nil, nil
}

func (c *blockingClientService) RawLogs(context.Context, container.Container, time.Time, time.Time, container.StdType) (io.ReadCloser, error) {
	return nil, nil
}

func (c *blockingClientService) SubscribeStats(context.Context, chan<- container.ContainerStat) {}

func (c *blockingClientService) SubscribeEvents(context.Context, chan<- container.ContainerEvent) {}

func (c *blockingClientService) SubscribeContainersStarted(context.Context, chan<- container.Container) {
}

func (c *blockingClientService) StreamLogs(context.Context, container.Container, time.Time, container.StdType, chan<- *container.LogEvent) error {
	return nil
}

func (c *blockingClientService) Attach(context.Context, container.Container, container.ExecEventReader, io.Writer) error {
	return nil
}

func (c *blockingClientService) Exec(context.Context, container.Container, []string, container.ExecEventReader, io.Writer) error {
	return nil
}
