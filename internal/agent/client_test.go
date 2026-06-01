package agent

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"os"
	"path"
	"testing"
	"time"

	"github.com/amir20/dozzle/internal/container"
	"github.com/amir20/dozzle/internal/notification/dispatcher"
	"github.com/amir20/dozzle/internal/utils"
	"github.com/amir20/dozzle/types"
	"github.com/go-faker/faker/v4"
	"github.com/go-faker/faker/v4/pkg/options"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	"google.golang.org/grpc"
	"google.golang.org/grpc/test/bufconn"
)

const bufSize = 1024 * 1024

var lis *bufconn.Listener
var certs tls.Certificate
var mockService *MockedClientService

type mockNotificationHandler struct{}

func (m *mockNotificationHandler) HandleNotificationConfig(subscriptions []types.SubscriptionConfig, dispatchers []types.DispatcherConfig) error {
	return nil
}

func (m *mockNotificationHandler) SetCloudDispatcher(d dispatcher.Dispatcher) {}
func (m *mockNotificationHandler) ClearCloudDispatcher()                      {}

func (m *mockNotificationHandler) GetNotificationStats() []types.SubscriptionStats {
	return nil
}

type captureNotificationHandler struct {
	subscriptions []types.SubscriptionConfig
	dispatchers   []types.DispatcherConfig
}

func (h *captureNotificationHandler) HandleNotificationConfig(subscriptions []types.SubscriptionConfig, dispatchers []types.DispatcherConfig) error {
	h.subscriptions = subscriptions
	h.dispatchers = dispatchers
	return nil
}

func (h *captureNotificationHandler) SetCloudDispatcher(d dispatcher.Dispatcher) {}
func (h *captureNotificationHandler) ClearCloudDispatcher()                      {}

func (h *captureNotificationHandler) GetNotificationStats() []types.SubscriptionStats {
	return nil
}

type MockedClientService struct {
	mock.Mock
}

func (m *MockedClientService) FindContainer(ctx context.Context, id string, labels container.ContainerLabels) (container.Container, error) {
	args := m.Called(ctx, id, labels)
	return args.Get(0).(container.Container), args.Error(1)
}

func (m *MockedClientService) ListContainers(ctx context.Context, filter container.ContainerLabels) ([]container.Container, error) {
	args := m.Called(ctx, filter)
	return args.Get(0).([]container.Container), args.Error(1)
}

func (m *MockedClientService) Host(ctx context.Context) (container.Host, error) {
	args := m.Called(ctx)
	return args.Get(0).(container.Host), args.Error(1)
}

func (m *MockedClientService) ContainerAction(ctx context.Context, c container.Container, action container.ContainerAction) error {
	args := m.Called(ctx, c, action)
	return args.Error(0)
}

func (m *MockedClientService) LogsBetweenDates(ctx context.Context, c container.Container, from time.Time, to time.Time, stdTypes container.StdType) (<-chan *container.LogEvent, error) {
	args := m.Called(ctx, c, from, to, stdTypes)
	return args.Get(0).(<-chan *container.LogEvent), args.Error(1)
}

func (m *MockedClientService) RawLogs(ctx context.Context, c container.Container, from time.Time, to time.Time, stdTypes container.StdType) (io.ReadCloser, error) {
	args := m.Called(ctx, c, from, to, stdTypes)
	return args.Get(0).(io.ReadCloser), args.Error(1)
}

func (m *MockedClientService) SubscribeStats(ctx context.Context, stats chan<- container.ContainerStat) {
	m.Called(ctx, stats)
}

func (m *MockedClientService) SubscribeEvents(ctx context.Context, events chan<- container.ContainerEvent) {
	m.Called(ctx, events)
}

func (m *MockedClientService) SubscribeContainersStarted(ctx context.Context, containers chan<- container.Container) {
	m.Called(ctx, containers)
}

func (m *MockedClientService) StreamLogs(ctx context.Context, c container.Container, from time.Time, stdTypes container.StdType, events chan<- *container.LogEvent) error {
	args := m.Called(ctx, c, from, stdTypes, events)
	return args.Error(0)
}

func (m *MockedClientService) Attach(ctx context.Context, c container.Container, events container.ExecEventReader, stdout io.Writer) error {
	args := m.Called(ctx, c, events, stdout)
	return args.Error(0)
}

func (m *MockedClientService) Exec(ctx context.Context, c container.Container, cmd []string, events container.ExecEventReader, stdout io.Writer) error {
	args := m.Called(ctx, c, cmd, events, stdout)
	return args.Error(0)
}

func (m *MockedClientService) UpdateContainer(ctx context.Context, c container.Container, progressCh chan<- container.UpdateProgress) (bool, error) {
	args := m.Called(ctx, c, progressCh)
	return args.Bool(0), args.Error(1)
}

var wantedContainer = container.Container{}

func init() {
	faker.FakeData(&wantedContainer, options.WithFieldsToIgnore("Stats", "MountStats", "Ports"))
	wantedContainer.FinishedAt = wantedContainer.FinishedAt.UTC()
	wantedContainer.Created = wantedContainer.Created.UTC()
	wantedContainer.StartedAt = wantedContainer.StartedAt.UTC()
	wantedContainer.Stats = utils.NewRingBuffer[container.ContainerStat](300)

	fmt.Printf("Fake data generated %+v", wantedContainer)
	lis = bufconn.Listen(bufSize)

	cwd, err := os.Getwd()
	if err != nil {
		panic(err)
	}
	root := path.Join(cwd, "../../")
	certs, err = tls.LoadX509KeyPair(path.Join(root, "shared_cert.pem"), path.Join(root, "shared_key.pem"))
	if err != nil {
		panic(err)
	}

	mockService = &MockedClientService{}
	mockService.On("ListContainers", mock.Anything, mock.Anything).Return([]container.Container{
		wantedContainer,
	}, nil)

	mockService.On("Host", mock.Anything).Return(container.Host{
		ID:       "localhost",
		Endpoint: "local",
		Name:     "local",
		Group:    "Development",
	}, nil)

	mockService.On("SubscribeEvents", mock.Anything, mock.AnythingOfType("chan<- container.ContainerEvent")).Return().Run(func(args mock.Arguments) {
		time.Sleep(5 * time.Second)
	})

	mockService.On("SubscribeStats", mock.Anything, mock.AnythingOfType("chan<- container.ContainerStat")).Return()

	mockService.On("SubscribeContainersStarted", mock.Anything, mock.AnythingOfType("chan<- container.Container")).Return()

	mockService.On("FindContainer", mock.Anything, "123456", mock.Anything).Return(wantedContainer, nil)

	mockService.On("Client").Return(nil)

	server, _ := NewServer(mockService, certs, "test", &mockNotificationHandler{})
	go server.Serve(lis)
}

func bufDialer(ctx context.Context, address string) (net.Conn, error) {
	return lis.Dial()
}

func TestFindContainer(t *testing.T) {
	rpc, err := NewClient("passthrough://bufnet", certs, grpc.WithContextDialer(bufDialer))
	if err != nil {
		t.Fatal(err)
	}

	c, _ := rpc.FindContainer(context.Background(), "123456", container.ContainerLabels{})

	assert.Equal(t, wantedContainer, c)
}

func TestListContainers(t *testing.T) {
	rpc, err := NewClient("passthrough://bufnet", certs, grpc.WithContextDialer(bufDialer))
	if err != nil {
		t.Fatal(err)
	}

	containers, _ := rpc.ListContainers(context.Background(), container.ContainerLabels{})

	assert.Equal(t, []container.Container{
		wantedContainer,
	}, containers)
}

func TestHostWithAgentMetadata(t *testing.T) {
	rpc, err := NewClient("passthrough://bufnet|Web-1|Production", certs, grpc.WithContextDialer(bufDialer))
	if err != nil {
		t.Fatal(err)
	}

	host, err := rpc.Host(context.Background())

	assert.NoError(t, err)
	assert.Equal(t, "passthrough://bufnet", host.Endpoint)
	assert.Equal(t, "Web-1", host.Name)
	assert.Equal(t, "Production", host.Group)
}

func TestHostUsesAdvertisedAgentGroup(t *testing.T) {
	rpc, err := NewClient("passthrough://bufnet", certs, grpc.WithContextDialer(bufDialer))
	if err != nil {
		t.Fatal(err)
	}

	host, err := rpc.Host(context.Background())

	assert.NoError(t, err)
	assert.Equal(t, "Development", host.Group)
}

func TestUpdateNotificationConfigPreservesNtfyDispatcherFields(t *testing.T) {
	listener := bufconn.Listen(bufSize)
	handler := &captureNotificationHandler{}
	server, err := NewServer(mockService, certs, "test", handler)
	assert.NoError(t, err)
	go server.Serve(listener)
	defer server.Stop()

	rpc, err := NewClient("passthrough://ntfy-config", certs, grpc.WithContextDialer(func(ctx context.Context, address string) (net.Conn, error) {
		return listener.Dial()
	}))
	assert.NoError(t, err)

	dispatchers := []types.DispatcherConfig{
		{
			ID:              7,
			Name:            "Ops ntfy",
			Type:            "ntfy",
			URL:             "https://ntfy.example.com",
			Topic:           "ops-alerts",
			Priority:        5,
			Tags:            []string{"rotating_light", "docker"},
			Token:           "tk_secret",
			TitleTemplate:   "[{{ .Container.HostName }}] {{ .Subscription.Name }}",
			MessageTemplate: "Alert: {{ .Subscription.Name }}\n\n{{ .Detail }}",
		},
	}

	err = rpc.UpdateNotificationConfig(context.Background(), nil, dispatchers)

	assert.NoError(t, err)
	assert.Equal(t, dispatchers, handler.dispatchers)
}

func TestUpdateNotificationConfigPreservesAlertSubscriptionFields(t *testing.T) {
	listener := bufconn.Listen(bufSize)
	handler := &captureNotificationHandler{}
	server, err := NewServer(mockService, certs, "test", handler)
	assert.NoError(t, err)
	go server.Serve(listener)
	defer server.Stop()

	rpc, err := NewClient("passthrough://subscription-config", certs, grpc.WithContextDialer(func(ctx context.Context, address string) (net.Conn, error) {
		return listener.Dial()
	}))
	assert.NoError(t, err)

	pausedUntil := time.Date(2026, time.June, 1, 10, 30, 0, 0, time.UTC)
	subscriptions := []types.SubscriptionConfig{
		{
			ID:                        42,
			Name:                      "Agent alert",
			Enabled:                   true,
			DispatcherID:              7,
			LogExpression:             "message contains 'error'",
			ContainerExpression:       "name == 'api'",
			MetricExpression:          "cpu > 90",
			EventExpression:           "event == 'die'",
			Cooldown:                  120,
			SampleWindow:              30,
			PausedUntil:               &pausedUntil,
			DeliveryDays:              []string{"mon", "fri"},
			NtfyTopic:                 "ops",
			NtfyPriority:              5,
			NtfyTags:                  []string{"docker", "agent"},
			BypassQuietHours:          true,
			QuietPriority:             1,
			HoldDuringQuiet:           true,
			HoldClearWindow:           60,
			BurstCount:                3,
			BurstWindow:               300,
			BurstPriority:             5,
			BurstNtfyTopic:            "ops-burst",
			UniqueKeyRegex:            `request_id=(\w+)`,
			UniqueWindow:              3600,
			UniqueThreshold:           2,
			WatchdogPattern:           "message contains 'ok'",
			WatchdogWindow:            600,
			WatchdogCooldown:          900,
			WatchdogTriggerMessage:    "service did not recover",
			WatchdogClearMessage:      "service recovered",
			RestartLoopEnabled:        true,
			RestartLoopStateWindow:    120,
			RestartLoopEventCount:     4,
			RestartLoopEventWindow:    300,
			RestartLoopCooldown:       600,
			RestartLoopTriggerMessage: "restart loop detected",
			QuietStackThreshold:       5,
			QuietStackWindow:          1800,
			AlertQuietEnabled:         true,
			AlertQuietStart:           "22:00",
			AlertQuietEnd:             "07:00",
			AlertQuietTimezone:        "Europe/Prague",
		},
	}

	err = rpc.UpdateNotificationConfig(context.Background(), subscriptions, nil)

	assert.NoError(t, err)
	assert.Equal(t, subscriptions, handler.subscriptions)
}

func TestHostWithAgentGroupAndDefaultName(t *testing.T) {
	rpc, err := NewClient("passthrough://bufnet||Production", certs, grpc.WithContextDialer(bufDialer))
	if err != nil {
		t.Fatal(err)
	}

	host, err := rpc.Host(context.Background())

	assert.NoError(t, err)
	assert.Equal(t, "passthrough://bufnet", host.Endpoint)
	assert.Equal(t, "local", host.Name)
	assert.Equal(t, "Production", host.Group)
}

func TestNewClientRejectsInvalidAgentEndpoint(t *testing.T) {
	_, err := NewClient("passthrough://bufnet|Web-1|Production|extra", certs, grpc.WithContextDialer(bufDialer))

	assert.Error(t, err)
}

func TestParseEndpoint(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantAddr  string
		wantName  string
		wantGroup string
		wantErr   bool
	}{
		{name: "address only", input: "host:7007", wantAddr: "host:7007"},
		{name: "address and name", input: "host:7007|web-1", wantAddr: "host:7007", wantName: "web-1"},
		{name: "address, name, group", input: "host:7007|web-1|prod", wantAddr: "host:7007", wantName: "web-1", wantGroup: "prod"},
		{name: "trailing empty group", input: "host:7007|web-1|", wantAddr: "host:7007", wantName: "web-1"},
		{name: "empty name with group", input: "host:7007||prod", wantAddr: "host:7007", wantGroup: "prod"},
		{name: "empty address rejected", input: "|web-1|prod", wantErr: true},
		{name: "too many segments rejected", input: "host:7007|web-1|prod|extra", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			addr, name, group, err := ParseEndpoint(tt.input)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, tt.wantAddr, addr)
			assert.Equal(t, tt.wantName, name)
			assert.Equal(t, tt.wantGroup, group)
		})
	}
}
