package messages

// messages that ship with TaskStatus objects

const (
	ContainersDisappeared    = "containers-disappeared"
	CreateBindingFailure     = "create-binding-failure"
	CreateBindingSuccess     = "create-binding-success"
	ExecutorUnregistered     = "executor-unregistered"
	ExecutorShutdown         = "executor-shutdown"
	LaunchTaskFailed         = "launch-task-failed"
	TaskKilled               = "task-killed"
	UnmarshalTaskDataFailure = "unmarshal-task-data-failure"
	TaskLostAck              = "task-lost-ack" // executor acknowledgement of forwarded TASK_LOST framework message
)
