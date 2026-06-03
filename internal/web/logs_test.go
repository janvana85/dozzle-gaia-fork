package web

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/amir20/dozzle/internal/container"
	"github.com/amir20/dozzle/internal/log_storage"
	container_support "github.com/amir20/dozzle/internal/support/container"
	support_web "github.com/amir20/dozzle/internal/support/web"
	"github.com/beme/abide"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func addAllLogLevels(q url.Values) {
	q.Add("levels", "error")
	q.Add("levels", "warn")
	q.Add("levels", "info")
	q.Add("levels", "debug")
	q.Add("levels", "trace")
	q.Add("levels", "fatal")
	q.Add("levels", "unknown")
}

func emptyContent() fstest.MapFS {
	return fstest.MapFS{"index.html": {Data: []byte("index page")}}
}

type gapFillHostService struct {
	HostService
	c             container.Container
	clientService container_support.ClientService
}

func (s *gapFillHostService) FindContainer(host string, id string, labels container.ContainerLabels) (*container_support.ContainerService, error) {
	return container_support.NewContainerService(s.clientService, s.c), nil
}

type gapFillClientService struct {
	container_support.ClientService
	releaseFetch chan struct{}
	fetchStarted chan struct{}
}

func (s *gapFillClientService) LogsBetweenDates(ctx context.Context, c container.Container, from time.Time, to time.Time, stdTypes container.StdType) (<-chan *container.LogEvent, error) {
	close(s.fetchStarted)
	<-s.releaseFetch
	ch := make(chan *container.LogEvent)
	close(ch)
	return ch, nil
}

func Test_handler_streamLogs_happy(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())

	id := "123456"
	req, err := http.NewRequestWithContext(ctx, "GET", "/api/hosts/localhost/containers/"+id+"/logs/stream", nil)

	q := req.URL.Query()
	q.Add("stdout", "true")
	q.Add("stderr", "true")
	addAllLogLevels(q)

	req.URL.RawQuery = q.Encode()
	require.NoError(t, err, "NewRequest should not return an error.")

	mockedClient := new(MockedClient)

	data := makeMessage("INFO Testing logs...\n", container.STDOUT)

	now := time.Now()

	mockedClient.On("FindContainer", mock.Anything, id).Return(container.Container{ID: id, Tty: false, Host: "localhost", StartedAt: now}, nil)
	mockedClient.On("ContainerLogs", mock.Anything, mock.Anything, now, container.STDALL).Return(io.NopCloser(bytes.NewReader(data)), nil).
		Run(func(args mock.Arguments) {
			go func() {
				time.Sleep(50 * time.Millisecond)
				cancel()
			}()
		})
	mockedClient.On("Host").Return(container.Host{
		ID: "localhost",
	})
	mockedClient.On("ListContainers", mock.Anything, mock.Anything).Return([]container.Container{
		{ID: id, Name: "test", Host: "localhost", State: "running"},
	}, nil)
	mockedClient.On("ContainerEvents", mock.Anything, mock.AnythingOfType("chan<- container.ContainerEvent")).Return(nil).Run(func(args mock.Arguments) {
		time.Sleep(50 * time.Millisecond)
	})

	handler := createDefaultHandler(mockedClient)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	body := regexp.MustCompile(`"time":"[^"]*"`).ReplaceAllString(rr.Body.String(), `"time":"<removed>"`)
	assert.Contains(t, body, `:ping`)
	assert.Contains(t, body, `data: {"t":"single","m":"INFO Testing logs...\n","ts":0,"id":3835490584,"l":"info","s":"stdout","c":"123456"}`)
	mockedClient.AssertExpectations(t)
}

func Test_handler_streamLogs_happy_with_id(t *testing.T) {
	id := "123456"
	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, "GET", "/api/hosts/localhost/containers/"+id+"/logs/stream", nil)
	q := req.URL.Query()
	q.Add("stdout", "true")
	q.Add("stderr", "true")
	addAllLogLevels(q)

	req.URL.RawQuery = q.Encode()
	require.NoError(t, err, "NewRequest should not return an error.")

	mockedClient := new(MockedClient)

	data := makeMessage("2020-05-13T18:55:37.772853839Z INFO Testing logs...\n", container.STDOUT)

	started := time.Date(2020, time.May, 13, 18, 55, 37, 772853839, time.UTC)

	mockedClient.On("FindContainer", mock.Anything, id).Return(container.Container{ID: id, Host: "localhost", StartedAt: started}, nil)
	mockedClient.On("ContainerLogs", mock.Anything, mock.Anything, started, container.STDALL).Return(io.NopCloser(bytes.NewReader(data)), nil).
		Run(func(args mock.Arguments) {
			go func() {
				time.Sleep(50 * time.Millisecond)
				cancel()
			}()
		})
	mockedClient.On("Host").Return(container.Host{
		ID: "localhost",
	})

	mockedClient.On("ListContainers", mock.Anything, mock.Anything).Return([]container.Container{
		{ID: id, Name: "test", Host: "localhost", State: "running"},
	}, nil)

	mockedClient.On("ContainerEvents", mock.Anything, mock.AnythingOfType("chan<- container.ContainerEvent")).Return(nil).Run(func(args mock.Arguments) {
		time.Sleep(50 * time.Millisecond)
	})

	handler := createDefaultHandler(mockedClient)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	body := regexp.MustCompile(`"time":"[^"]*"`).ReplaceAllString(rr.Body.String(), `"time":"<removed>"`)
	assert.Contains(t, body, `:ping`)
	assert.Contains(t, body, `data: {"t":"single","m":"INFO Testing logs...","rm":"INFO Testing logs...","ts":1589396137772,"id":2908612274,"l":"info","s":"stdout","c":"123456"}`)
	assert.Contains(t, body, `id: 1589396137772`)
	mockedClient.AssertExpectations(t)
}

func Test_handler_streamLogs_happy_container_stopped(t *testing.T) {
	id := "123456"
	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, "GET", "/api/hosts/localhost/containers/"+id+"/logs/stream", nil)
	q := req.URL.Query()
	q.Add("stdout", "true")
	q.Add("stderr", "true")
	addAllLogLevels(q)

	req.URL.RawQuery = q.Encode()
	require.NoError(t, err, "NewRequest should not return an error.")

	started := time.Date(2020, time.May, 13, 18, 55, 37, 772853839, time.UTC)
	mockedClient := new(MockedClient)
	mockedClient.On("FindContainer", mock.Anything, id).Return(container.Container{ID: id, Host: "localhost", StartedAt: started}, nil)
	mockedClient.On("ContainerLogs", mock.Anything, id, started, container.STDALL).Return(io.NopCloser(strings.NewReader("")), io.EOF).
		Run(func(args mock.Arguments) {
			go func() {
				time.Sleep(50 * time.Millisecond)
				cancel()
			}()
		})
	mockedClient.On("Host").Return(container.Host{
		ID: "localhost",
	})
	mockedClient.On("ListContainers", mock.Anything, mock.Anything).Return([]container.Container{
		{ID: id, Name: "test", Host: "localhost", State: "running"},
	}, nil)
	mockedClient.On("ContainerEvents", mock.Anything, mock.AnythingOfType("chan<- container.ContainerEvent")).Return(nil)

	handler := createDefaultHandler(mockedClient)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	body := regexp.MustCompile(`"time":"[^"]*"`).ReplaceAllString(rr.Body.String(), `"time":"<removed>"`)
	assert.Contains(t, body, `:ping`)
	mockedClient.AssertExpectations(t)
}

func Test_handler_streamLogs_error_reading(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	id := "123456"
	req, err := http.NewRequestWithContext(ctx, "GET", "/api/hosts/localhost/containers/"+id+"/logs/stream", nil)
	q := req.URL.Query()
	q.Add("stdout", "true")
	q.Add("stderr", "true")
	addAllLogLevels(q)

	req.URL.RawQuery = q.Encode()
	require.NoError(t, err, "NewRequest should not return an error.")

	started := time.Date(2020, time.May, 13, 18, 55, 37, 772853839, time.UTC)
	mockedClient := new(MockedClient)
	mockedClient.On("FindContainer", mock.Anything, id).Return(container.Container{ID: id, Host: "localhost", StartedAt: started}, nil)
	mockedClient.On("ContainerLogs", mock.Anything, id, started, container.STDALL).Return(io.NopCloser(strings.NewReader("")), errors.New("test error")).
		Run(func(args mock.Arguments) {
			go func() {
				time.Sleep(50 * time.Millisecond)
				cancel()
			}()
		})
	mockedClient.On("Host").Return(container.Host{
		ID: "localhost",
	})
	mockedClient.On("ListContainers", mock.Anything, mock.Anything).Return([]container.Container{
		{ID: id, Name: "test", Host: "localhost", State: "running"},
	}, nil)
	mockedClient.On("ContainerEvents", mock.Anything, mock.AnythingOfType("chan<- container.ContainerEvent")).Return(nil)

	handler := createDefaultHandler(mockedClient)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	reader := strings.NewReader(regexp.MustCompile(`"time":"[^"]*"`).ReplaceAllString(rr.Body.String(), `"time":"<removed>"`))
	abide.AssertReader(t, t.Name(), reader)
	mockedClient.AssertExpectations(t)
}

func Test_handler_streamLogs_error_std(t *testing.T) {
	id := "123456"
	req, err := http.NewRequest("GET", "/api/hosts/localhost/containers/"+id+"/logs/stream", nil)

	require.NoError(t, err, "NewRequest should not return an error.")

	mockedClient := new(MockedClient)
	mockedClient.On("FindContainer", mock.Anything, id).Return(container.Container{ID: id, Host: "localhost"}, nil)
	mockedClient.On("Host").Return(container.Host{
		ID: "localhost",
	})
	mockedClient.On("ListContainers", mock.Anything, mock.Anything).Return([]container.Container{
		{ID: id, Name: "test", Host: "localhost", State: "running"},
	}, nil)
	mockedClient.On("ContainerEvents", mock.Anything, mock.AnythingOfType("chan<- container.ContainerEvent")).Return(nil).
		Run(func(args mock.Arguments) {
			time.Sleep(50 * time.Millisecond)
		})

	handler := createDefaultHandler(mockedClient)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusBadRequest, rr.Code)
	assert.Contains(t, rr.Body.String(), "stdout or stderr is required")
}

func Test_handler_between_dates(t *testing.T) {
	id := "123456"
	req, err := http.NewRequest("GET", "/api/hosts/localhost/containers/"+id+"/logs", nil)
	require.NoError(t, err, "NewRequest should not return an error.")

	from, _ := time.Parse(time.RFC3339, "2018-01-01T00:00:00Z")
	to, _ := time.Parse(time.RFC3339, "2018-01-01T10:00:00Z")

	q := req.URL.Query()
	q.Add("from", from.Format(time.RFC3339))
	q.Add("to", to.Format(time.RFC3339))
	q.Add("stdout", "true")
	q.Add("stderr", "true")
	q.Add("levels", "info")

	req.URL.RawQuery = q.Encode()

	mockedClient := new(MockedClient)

	first := makeMessage("2020-05-13T18:55:37.772853839Z INFO Testing stdout logs...\n", container.STDOUT)
	second := makeMessage("2020-05-13T18:56:37.772853839Z INFO Testing stderr logs...\n", container.STDERR)
	data := append(first, second...)

	mockedClient.On("ContainerLogsBetweenDates", mock.Anything, id, from, to, container.STDALL).Return(io.NopCloser(bytes.NewReader(data)), nil)
	mockedClient.On("FindContainer", mock.Anything, id).Return(container.Container{ID: id}, nil)
	mockedClient.On("Host").Return(container.Host{
		ID: "localhost",
	})
	mockedClient.On("ListContainers", mock.Anything, mock.Anything).Return([]container.Container{
		{ID: id, Name: "test", Host: "localhost", State: "running"},
	}, nil)
	mockedClient.On("ContainerEvents", mock.Anything, mock.AnythingOfType("chan<- container.ContainerEvent")).Return(nil)

	handler := createDefaultHandler(mockedClient)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	reader := strings.NewReader(regexp.MustCompile(`"time":"[^"]*"`).ReplaceAllString(rr.Body.String(), `"time":"<removed>"`))
	abide.AssertReader(t, t.Name(), reader)
	mockedClient.AssertExpectations(t)
}

func Test_handler_between_dates_empty_cache_range_returns_gap_without_waiting_for_docker(t *testing.T) {
	id := "123456"
	req, err := http.NewRequest("GET", "/api/hosts/localhost/containers/"+id+"/logs", nil)
	require.NoError(t, err, "NewRequest should not return an error.")

	from, _ := time.Parse(time.RFC3339, "2018-01-01T00:00:00Z")
	to, _ := time.Parse(time.RFC3339, "2018-01-01T10:00:00Z")

	q := req.URL.Query()
	q.Add("from", from.Format(time.RFC3339))
	q.Add("to", to.Format(time.RFC3339))
	q.Add("stdout", "true")
	q.Add("stderr", "true")
	q.Add("levels", "info")
	req.URL.RawQuery = q.Encode()

	c := container.Container{ID: id, Host: "localhost"}
	store := log_storage.NewStore(t.TempDir())
	err = store.AppendForContainer(c, &container.LogEvent{
		Type:        container.LogTypeSingle,
		Message:     "older cached log",
		RawMessage:  "older cached log",
		Timestamp:   time.Date(2017, time.December, 31, 10, 0, 0, 0, time.UTC).UnixMilli(),
		Id:          1,
		Level:       "info",
		Stream:      "stdout",
		ContainerID: id,
	})
	require.NoError(t, err)

	releaseFetch := make(chan struct{})
	fetchStarted := make(chan struct{})
	client := &gapFillClientService{releaseFetch: releaseFetch, fetchStarted: fetchStarted}
	hostService := &gapFillHostService{
		c:             c,
		clientService: client,
	}
	h := &handler{
		hostService: hostService,
		content:     emptyContent(),
		config:      &Config{Base: "/", Authorization: Authorization{Provider: NONE}, LogStore: store},
		logStore:    store,
	}
	routeContext := chi.NewRouteContext()
	routeContext.URLParams.Add("host", "localhost")
	routeContext.URLParams.Add("id", id)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, routeContext))
	rr := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		h.fetchLogsBetweenDates(rr, req)
		close(done)
	}()

	select {
	case <-done:
	case <-fetchStarted:
		select {
		case <-done:
		case <-time.After(100 * time.Millisecond):
			close(releaseFetch)
			t.Fatal("handler waited for Docker instead of returning a cache gap marker")
		}
	case <-time.After(5 * time.Second):
		close(releaseFetch)
		t.Fatal("handler did not return a cache gap marker")
	}
	close(releaseFetch)

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Contains(t, rr.Body.String(), `"t":"cache-gap"`)
	assert.Contains(t, rr.Body.String(), `"m":"not found in cache, fetching from docker logs"`)
	select {
	case <-fetchStarted:
	case <-time.After(time.Second):
		t.Fatal("background cache fill did not start")
	}
}

func Test_handler_between_dates_leading_cache_gap_when_range_starts_before_first_cached_log(t *testing.T) {
	id := "123456"
	req, err := http.NewRequest("GET", "/api/hosts/localhost/containers/"+id+"/logs", nil)
	require.NoError(t, err, "NewRequest should not return an error.")

	from, _ := time.Parse(time.RFC3339, "2018-01-01T00:00:00Z")
	firstTime, _ := time.Parse(time.RFC3339, "2018-01-01T10:00:00Z")
	to, _ := time.Parse(time.RFC3339, "2018-01-01T10:00:00.050Z")

	q := req.URL.Query()
	q.Add("from", from.Format(time.RFC3339))
	q.Add("to", to.Format(time.RFC3339Nano))
	q.Add("stdout", "true")
	q.Add("stderr", "true")
	q.Add("levels", "info")
	req.URL.RawQuery = q.Encode()

	c := container.Container{ID: id, Host: "unknown"}
	store := log_storage.NewStore(t.TempDir())
	logEvents := make(chan *container.LogEvent)
	store.Start(t.Context(), logEvents)
	logEvents <- &container.LogEvent{
		Type:        container.LogTypeSingle,
		Message:     "first cached log",
		RawMessage:  "first cached log",
		Timestamp:   firstTime.UnixMilli(),
		Id:          1,
		Level:       "info",
		Stream:      "stdout",
		ContainerID: id,
	}
	close(logEvents)
	time.Sleep(50 * time.Millisecond)

	releaseFetch := make(chan struct{})
	fetchStarted := make(chan struct{})
	client := &gapFillClientService{releaseFetch: releaseFetch, fetchStarted: fetchStarted}
	hostService := &gapFillHostService{
		c:             c,
		clientService: client,
	}
	h := &handler{
		hostService: hostService,
		content:     emptyContent(),
		config:      &Config{Base: "/", Authorization: Authorization{Provider: NONE}, LogStore: store},
		logStore:    store,
	}
	routeContext := chi.NewRouteContext()
	routeContext.URLParams.Add("host", "localhost")
	routeContext.URLParams.Add("id", id)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, routeContext))
	rr := httptest.NewRecorder()

	h.fetchLogsBetweenDates(rr, req)
	close(releaseFetch)

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Contains(t, rr.Body.String(), `"t":"cache-gap"`)
	assert.Contains(t, rr.Body.String(), `"from":"2018-01-01T00:00:00Z"`)
	assert.Contains(t, rr.Body.String(), `"to":"2018-01-01T10:00:00Z"`)
	assert.Contains(t, rr.Body.String(), `"m":"first cached log"`)
	select {
	case <-fetchStarted:
	case <-time.After(time.Second):
		t.Fatal("background cache fill did not start for the leading gap")
	}
}

func Test_handler_between_dates_with_fill(t *testing.T) {
	id := "123456"
	req, err := http.NewRequest("GET", "/api/hosts/localhost/containers/"+id+"/logs", nil)
	require.NoError(t, err, "NewRequest should not return an error.")

	from, _ := time.Parse(time.RFC3339, "2018-01-01T00:00:00Z")
	to, _ := time.Parse(time.RFC3339, "2018-01-01T10:00:00Z")

	q := req.URL.Query()
	q.Add("from", from.Format(time.RFC3339))
	q.Add("to", to.Format(time.RFC3339))
	q.Add("stdout", "true")
	q.Add("stderr", "true")
	q.Add("fill", "true")
	q.Add("levels", "info")
	q.Add("min", "10")

	req.URL.RawQuery = q.Encode()

	mockedClient := new(MockedClient)

	first := makeMessage("2020-05-13T18:55:37.772853839Z INFO Testing stdout logs...\n", container.STDOUT)
	second := makeMessage("2020-05-13T18:56:37.772853839Z INFO Testing stderr logs...\n", container.STDERR)
	data := append(first, second...)

	mockedClient.On("ContainerLogsBetweenDates", mock.Anything, id, from, to, container.STDALL).
		Return(io.NopCloser(bytes.NewReader([]byte{})), nil).
		Once()

	mockedClient.On("ContainerLogsBetweenDates", mock.Anything, id, time.Date(2017, time.December, 31, 14, 0, 0, 0, time.UTC), to, container.STDALL).
		Return(io.NopCloser(bytes.NewReader(data)), nil).
		Once()

	mockedClient.On("ContainerLogsBetweenDates", mock.Anything, id, time.Date(2017, time.December, 30, 18, 0, 0, 0, time.UTC), to, container.STDALL).
		Return(io.NopCloser(bytes.NewReader(data)), nil).
		Once()

	mockedClient.On("FindContainer", mock.Anything, id).Return(container.Container{ID: id, Created: time.Date(2017, time.December, 31, 10, 0, 0, 0, time.UTC)}, nil)
	mockedClient.On("Host").Return(container.Host{ID: "localhost"})

	mockedClient.On("ListContainers", mock.Anything, mock.Anything).Return([]container.Container{
		{ID: id, Name: "test", Host: "localhost", State: "running"},
	}, nil)

	mockedClient.On("ContainerEvents", mock.Anything, mock.AnythingOfType("chan<- container.ContainerEvent")).Return(nil)

	handler := createDefaultHandler(mockedClient)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	reader := strings.NewReader(regexp.MustCompile(`"time":"[^"]*"`).ReplaceAllString(rr.Body.String(), `"time":"<removed>"`))
	abide.AssertReader(t, t.Name(), reader)
	mockedClient.AssertExpectations(t)
}

func Test_handler_between_dates_with_everything_complex(t *testing.T) {
	id := "123456"
	req, err := http.NewRequest("GET", "/api/hosts/localhost/containers/"+id+"/logs", nil)
	require.NoError(t, err, "NewRequest should not return an error.")

	q := req.URL.Query()
	q.Add("jsonOnly", "true")
	q.Add("stdout", "true")
	q.Add("stderr", "true")
	q.Add("everything", "true")

	req.URL.RawQuery = q.Encode()

	mockedClient := new(MockedClient)

	first := makeMessage("2020-05-13T18:55:37.772853839Z INFO Testing stdout logs...\n", container.STDOUT)
	second := makeMessage("2020-05-13T18:56:37.772853839Z {\"msg\":\"a complex log message\"}\n", container.STDOUT)
	data := append(first, second...)

	mockedClient.On("ContainerLogsBetweenDates", mock.Anything, id, mock.Anything, mock.Anything, container.STDALL).
		Return(io.NopCloser(bytes.NewReader(data)), nil).
		Once()
	mockedClient.On("FindContainer", mock.Anything, id).Return(container.Container{ID: id}, nil)
	mockedClient.On("Host").Return(container.Host{
		ID: "localhost",
	})
	mockedClient.On("ListContainers", mock.Anything, mock.Anything).Return([]container.Container{
		{ID: id, Name: "test", Host: "localhost", State: "running"},
	}, nil)
	mockedClient.On("ContainerEvents", mock.Anything, mock.AnythingOfType("chan<- container.ContainerEvent")).Return(nil)

	handler := createDefaultHandler(mockedClient)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	reader := strings.NewReader(regexp.MustCompile(`"time":"[^"]*"`).ReplaceAllString(rr.Body.String(), `"time":"<removed>"`))
	abide.AssertReader(t, t.Name(), reader)
	mockedClient.AssertExpectations(t)
}

func Test_matchesFilter_inverse(t *testing.T) {
	levels := map[string]struct{}{"info": {}}

	regex, err := support_web.ParseRegex("INFO")
	require.NoError(t, err)

	regex2, err := support_web.ParseRegex("ERROR")
	require.NoError(t, err)

	tests := []struct {
		name    string
		event   *container.LogEvent
		regex   *regexp.Regexp
		levels  map[string]struct{}
		inverse bool
		want    bool
	}{
		{
			name:    "normal mode: matching regex and level passes",
			event:   &container.LogEvent{Message: "INFO: all good", Level: "info"},
			regex:   regex,
			levels:  levels,
			inverse: false,
			want:    true,
		},
		{
			name:    "normal mode: non-matching regex fails",
			event:   &container.LogEvent{Message: "INFO: all good", Level: "info"},
			regex:   regex2,
			levels:  levels,
			inverse: false,
			want:    false,
		},
		{
			name:    "inverse mode: matching regex fails (excluded)",
			event:   &container.LogEvent{Message: "INFO: all good", Level: "info"},
			regex:   regex,
			levels:  levels,
			inverse: true,
			want:    false,
		},
		{
			name:    "inverse mode: non-matching regex passes (not excluded)",
			event:   &container.LogEvent{Message: "INFO: all good", Level: "info"},
			regex:   regex2,
			levels:  levels,
			inverse: true,
			want:    true,
		},
		{
			name:    "no regex: inverse is a no-op, level check still applies",
			event:   &container.LogEvent{Message: "INFO: all good", Level: "info"},
			regex:   nil,
			levels:  levels,
			inverse: true,
			want:    true,
		},
		{
			name:    "inverse mode: wrong level fails regardless of regex",
			event:   &container.LogEvent{Message: "ERROR: oops", Level: "error"},
			regex:   regex2,
			levels:  levels,
			inverse: true,
			want:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := matchesFilter(tt.event, tt.regex, tt.levels, tt.inverse)
			assert.Equal(t, tt.want, got)
		})
	}
}

func makeMessage(message string, stream container.StdType) []byte {
	data := make([]byte, 8)
	binary.BigEndian.PutUint32(data[4:], uint32(len(message)))
	data[0] = byte(stream / 2)
	data = append(data, []byte(message)...)

	return data
}
