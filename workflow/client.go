package workflow

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/TFMV/pulse/proto"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
)

// TemporalConfig holds configuration for connecting to Temporal
type TemporalConfig struct {
	// HostPort is the Temporal server address (default: "localhost:7233")
	HostPort string `yaml:"host_port"`

	// Namespace is the Temporal namespace (default: "default")
	Namespace string `yaml:"namespace"`

	// TaskQueue is the name of the task queue (default: "pulse-payment-tasks")
	TaskQueue string `yaml:"task_queue"`

	// WorkflowExecutionTimeout is the maximum time a workflow can run (default: 5 minutes)
	WorkflowExecutionTimeout time.Duration `yaml:"workflow_execution_timeout"`

	// WorkerCount is the number of worker goroutines (default: 10)
	WorkerCount int `yaml:"worker_count"`

	// Enabled controls whether Temporal workflow orchestration is enabled
	Enabled bool `yaml:"enabled"`
}

// DefaultConfig returns the default Temporal configuration
func DefaultConfig() TemporalConfig {
	return TemporalConfig{
		HostPort:                 "localhost:7233",
		Namespace:                "default",
		TaskQueue:                "pulse-payment-tasks",
		WorkflowExecutionTimeout: 5 * time.Minute,
		WorkerCount:              10,
		Enabled:                  false,
	}
}

// Orchestrator manages the Temporal client, workers and workflow execution
type Orchestrator struct {
	config  TemporalConfig
	client  client.Client
	worker  worker.Worker
	started bool
}

// NewOrchestrator creates a new Temporal orchestrator
func NewOrchestrator(config TemporalConfig) (*Orchestrator, error) {
	if !config.Enabled {
		return &Orchestrator{
			config:  config,
			started: false,
		}, nil
	}

	// Create Temporal client
	c, err := client.NewClient(client.Options{
		HostPort:  config.HostPort,
		Namespace: config.Namespace,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create Temporal client: %w", err)
	}

	return &Orchestrator{
		config:  config,
		client:  c,
		started: false,
	}, nil
}

// RegisterWorkflowsAndActivities registers the payment workflows and activities with Temporal
func (o *Orchestrator) RegisterWorkflowsAndActivities(
	activities *Activities,
	getRegionFunc func(string) string,
) error {
	if !o.config.Enabled || o.client == nil {
		return nil
	}

	// Create a worker
	w := worker.New(o.client, o.config.TaskQueue, worker.Options{
		MaxConcurrentActivityExecutionSize: o.config.WorkerCount,
	})

	// Register workflows
	w.RegisterWorkflow(NewPaymentWorkflow(getRegionFunc).Execute)

	// Register activities
	w.RegisterActivity(activities.ProcessAuth)
	w.RegisterActivity(activities.CheckTransaction)
	w.RegisterActivity(activities.LogTransaction)

	o.worker = w
	return nil
}

// Start begins the Temporal worker processing
func (o *Orchestrator) Start() error {
	if !o.config.Enabled || o.worker == nil {
		log.Println("Temporal orchestration is disabled or not configured")
		return nil
	}

	if o.started {
		return nil
	}

	// Start worker
	err := o.worker.Start()
	if err != nil {
		return fmt.Errorf("failed to start Temporal worker: %w", err)
	}

	o.started = true
	log.Printf("Temporal worker started with task queue: %s", o.config.TaskQueue)
	return nil
}

// ExecutePaymentWorkflow starts a new payment workflow for a transaction
func (o *Orchestrator) ExecutePaymentWorkflow(
	ctx context.Context,
	request *proto.AuthRequest,
) (client.WorkflowRun, error) {
	if !o.config.Enabled || o.client == nil {
		return nil, fmt.Errorf("temporal orchestration is disabled or not configured")
	}

	// Create a workflow ID based on the transaction STAN
	workflowID := fmt.Sprintf("payment-%s-%s", request.Stan, time.Now().Format("20060102-150405"))

	// Start the workflow execution
	options := client.StartWorkflowOptions{
		ID:                       workflowID,
		TaskQueue:                o.config.TaskQueue,
		WorkflowExecutionTimeout: o.config.WorkflowExecutionTimeout,
		SearchAttributes: map[string]interface{}{
			"TransactionID": request.Stan,
			"WorkflowType":  "PaymentWorkflow",
		},
	}

	execution, err := o.client.ExecuteWorkflow(ctx, options, "Execute", request)
	if err != nil {
		return nil, fmt.Errorf("failed to execute payment workflow: %w", err)
	}

	log.Printf("Started payment workflow for transaction %s with execution ID: %s",
		request.Stan, execution.GetID())

	return execution, nil
}

// ExecutePaymentWorkflowSync executes a payment workflow and waits for the result
func (o *Orchestrator) ExecutePaymentWorkflowSync(
	ctx context.Context,
	request *proto.AuthRequest,
) (*proto.AuthResponse, error) {
	if !o.config.Enabled || o.client == nil {
		return nil, fmt.Errorf("temporal orchestration is disabled or not configured")
	}

	execution, err := o.ExecutePaymentWorkflow(ctx, request)
	if err != nil {
		return nil, err
	}

	// Wait for the workflow to complete
	var result proto.AuthResponse
	err = execution.Get(ctx, &result)
	if err != nil {
		return nil, fmt.Errorf("workflow execution failed: %w", err)
	}

	return &result, nil
}

// Close cleans up the Temporal client and worker
func (o *Orchestrator) Close() {
	if o.worker != nil && o.started {
		o.worker.Stop()
		log.Println("Temporal worker stopped")
	}

	if o.client != nil {
		o.client.Close()
		log.Println("Temporal client closed")
	}
}
