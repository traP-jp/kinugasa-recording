package workercontrol

import (
	"sync"

	"google.golang.org/protobuf/proto"

	workerv1 "github.com/traP-jp/kinugasa-recording/gen/console_video_worker/v1"
	"github.com/traP-jp/kinugasa-recording/internal/shared/workerprotocol"
)

type Registry struct {
	mu      sync.Mutex
	workers map[string]*connection
	cameras map[string]*connection
}

type connection struct {
	workerID string
	cameraID string
	abort    chan struct{}
	commands chan *workerv1.WorkerCommand
	once     sync.Once
}

type Lease struct {
	registry   *Registry
	connection *connection
}

func NewRegistry() *Registry {
	return &Registry{
		workers: make(map[string]*connection),
		cameras: make(map[string]*connection),
	}
}

func (r *Registry) Register(workerID, cameraID string) *Lease {
	r.mu.Lock()
	defer r.mu.Unlock()
	if previous := r.workers[workerID]; previous != nil {
		r.detach(previous)
	}
	if previous := r.cameras[cameraID]; previous != nil {
		r.detach(previous)
	}
	connection := &connection{
		workerID: workerID,
		cameraID: cameraID,
		abort:    make(chan struct{}),
		commands: make(chan *workerv1.WorkerCommand, 32),
	}
	r.workers[workerID] = connection
	r.cameras[cameraID] = connection
	return &Lease{registry: r, connection: connection}
}

func (r *Registry) Enqueue(cameraID string, command *workerv1.WorkerCommand) bool {
	if workerprotocol.ValidateWorkerCommand(command) != nil {
		return false
	}
	r.mu.Lock()
	connection := r.cameras[cameraID]
	if connection == nil {
		r.mu.Unlock()
		return false
	}
	select {
	case connection.commands <- proto.Clone(command).(*workerv1.WorkerCommand):
		r.mu.Unlock()
		return true
	default:
		r.mu.Unlock()
		return false
	}
}

func (r *Registry) detach(connection *connection) {
	if r.workers[connection.workerID] == connection {
		delete(r.workers, connection.workerID)
	}
	if r.cameras[connection.cameraID] == connection {
		delete(r.cameras, connection.cameraID)
	}
	connection.stop()
}

func (l *Lease) Aborted() <-chan struct{} {
	return l.connection.abort
}

func (l *Lease) Commands() <-chan *workerv1.WorkerCommand {
	return l.connection.commands
}

func (l *Lease) Close() {
	r := l.registry
	r.mu.Lock()
	defer r.mu.Unlock()
	r.detach(l.connection)
}

func (c *connection) stop() {
	c.once.Do(func() { close(c.abort) })
}
