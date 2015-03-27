package meta

// keys for things that we store
const (
	//TODO(jdef) this should also be a format instead of a fixed path
	FrameworkIDKey        = "/mesos/k8sm/frameworkid"
	DefaultElectionFormat = "/mesos/k8sm/framework/%s/leader"
)
