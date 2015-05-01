package meta

// kubernetes api object annotations
const (
	BindingHostKey           = "k8s.mesosphere.io/bindingHost"
	TaskIdKey                = "k8s.mesosphere.io/taskId"
	SlaveIdKey               = "k8s.mesosphere.io/slaveId"
	OfferIdKey               = "k8s.mesosphere.io/offerId"
	ExecutorIdKey            = "k8s.mesosphere.io/executorId"
	PortMappingKeyPrefix     = "k8s.mesosphere.io/port_"
	PortMappingKeyFormat     = PortMappingKeyPrefix + "%s_%d"
	PortNameMappingKeyPrefix = "k8s.mesosphere.io/portName_"
	PortNameMappingKeyFormat = PortNameMappingKeyPrefix + "%s_%s"
)
