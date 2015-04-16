package scheduler

import (
	"github.com/stretchr/testify/assert"
	"testing"
)

// Check that same slave is only added once.
func TestSlaveStorage_checkAndAdd(t *testing.T) {
	assert := assert.New(t)

	slaveStorage := newSlaveStorage()
	assert.Equal(0, len(slaveStorage.slaves))

	slaveId := "slave1"
	slaveHostname := "slave1Hostname"
	slaveStorage.checkAndAdd(slaveId, slaveHostname)
	assert.Equal(1, len(slaveStorage.getSlaveIds()))

	slaveStorage.checkAndAdd(slaveId, slaveHostname)
	assert.Equal(1, len(slaveStorage.getSlaveIds()))
}

// Check that getSlave returns notExist for nonexisting slave.
func TestSlaveStorage_getSlave(t *testing.T) {
	assert := assert.New(t)

	slaveStorage := newSlaveStorage()
	assert.Equal(0, len(slaveStorage.slaves))

	slaveId := "slave1"
	slaveHostname := "slave1Hostname"

	_, exists := slaveStorage.getSlave(slaveId)
	assert.Equal(false, exists)

	slaveStorage.checkAndAdd(slaveId, slaveHostname)
	assert.Equal(1, len(slaveStorage.getSlaveIds()))

	_, exists = slaveStorage.getSlave(slaveId)
	assert.Equal(true, exists)
}

// Check that getSlaveIds returns array with all slaveIds.
func TestSlaveStorage_getSlaveIds(t *testing.T) {
	assert := assert.New(t)

	slaveStorage := newSlaveStorage()
	assert.Equal(0, len(slaveStorage.slaves))

	slaveId := "1"
	slaveHostname := "hn1"
	slaveStorage.checkAndAdd(slaveId, slaveHostname)
	assert.Equal(1, len(slaveStorage.getSlaveIds()))

	slaveId = "2"
	slaveHostname = "hn2"
	slaveStorage.checkAndAdd(slaveId, slaveHostname)
	assert.Equal(2, len(slaveStorage.getSlaveIds()))

	slaveIds := slaveStorage.getSlaveIds()

	slaveIdsMap := make(map[string]bool, len(slaveIds))
	for _, s := range slaveIds {
		slaveIdsMap[s] = true
	}

	_, ok := slaveIdsMap["1"]
	assert.Equal(ok, true)

	_, ok = slaveIdsMap["2"]
	assert.Equal(ok, true)

}
