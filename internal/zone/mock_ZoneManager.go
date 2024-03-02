// Code generated by mockery v2.40.3. DO NOT EDIT.

package zone

import (
	context "context"
	election_agent_v1 "election-agent/proto/election_agent/v1"

	mock "github.com/stretchr/testify/mock"
)

// MockZoneManager is an autogenerated mock type for the ZoneManager type
type MockZoneManager struct {
	mock.Mock
}

// GetActiveZone provides a mock function with given fields:
func (_m *MockZoneManager) GetActiveZone() (string, error) {
	ret := _m.Called()

	if len(ret) == 0 {
		panic("no return value specified for GetActiveZone")
	}

	var r0 string
	var r1 error
	if rf, ok := ret.Get(0).(func() (string, error)); ok {
		return rf()
	}
	if rf, ok := ret.Get(0).(func() string); ok {
		r0 = rf()
	} else {
		r0 = ret.Get(0).(string)
	}

	if rf, ok := ret.Get(1).(func() error); ok {
		r1 = rf()
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// GetAgentState provides a mock function with given fields:
func (_m *MockZoneManager) GetAgentState() (string, error) {
	ret := _m.Called()

	if len(ret) == 0 {
		panic("no return value specified for GetAgentState")
	}

	var r0 string
	var r1 error
	if rf, ok := ret.Get(0).(func() (string, error)); ok {
		return rf()
	}
	if rf, ok := ret.Get(0).(func() string); ok {
		r0 = rf()
	} else {
		r0 = ret.Get(0).(string)
	}

	if rf, ok := ret.Get(1).(func() error); ok {
		r1 = rf()
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// GetMode provides a mock function with given fields:
func (_m *MockZoneManager) GetMode() string {
	ret := _m.Called()

	if len(ret) == 0 {
		panic("no return value specified for GetMode")
	}

	var r0 string
	if rf, ok := ret.Get(0).(func() string); ok {
		r0 = rf()
	} else {
		r0 = ret.Get(0).(string)
	}

	return r0
}

// GetPeerStates provides a mock function with given fields:
func (_m *MockZoneManager) GetPeerStates() ([]*election_agent_v1.AgentState, error) {
	ret := _m.Called()

	if len(ret) == 0 {
		panic("no return value specified for GetPeerStates")
	}

	var r0 []*election_agent_v1.AgentState
	var r1 error
	if rf, ok := ret.Get(0).(func() ([]*election_agent_v1.AgentState, error)); ok {
		return rf()
	}
	if rf, ok := ret.Get(0).(func() []*election_agent_v1.AgentState); ok {
		r0 = rf()
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).([]*election_agent_v1.AgentState)
		}
	}

	if rf, ok := ret.Get(1).(func() error); ok {
		r1 = rf()
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// GetZoomEnable provides a mock function with given fields:
func (_m *MockZoneManager) GetZoomEnable() (bool, error) {
	ret := _m.Called()

	if len(ret) == 0 {
		panic("no return value specified for GetZoomEnable")
	}

	var r0 bool
	var r1 error
	if rf, ok := ret.Get(0).(func() (bool, error)); ok {
		return rf()
	}
	if rf, ok := ret.Get(0).(func() bool); ok {
		r0 = rf()
	} else {
		r0 = ret.Get(0).(bool)
	}

	if rf, ok := ret.Get(1).(func() error); ok {
		r1 = rf()
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// SetAgentState provides a mock function with given fields: state
func (_m *MockZoneManager) SetAgentState(state string) error {
	ret := _m.Called(state)

	if len(ret) == 0 {
		panic("no return value specified for SetAgentState")
	}

	var r0 error
	if rf, ok := ret.Get(0).(func(string) error); ok {
		r0 = rf(state)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// SetMode provides a mock function with given fields: mode
func (_m *MockZoneManager) SetMode(mode string) {
	_m.Called(mode)
}

// SetPeerStates provides a mock function with given fields: state
func (_m *MockZoneManager) SetPeerStates(state string) error {
	ret := _m.Called(state)

	if len(ret) == 0 {
		panic("no return value specified for SetPeerStates")
	}

	var r0 error
	if rf, ok := ret.Get(0).(func(string) error); ok {
		r0 = rf(state)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// SetZoomEnable provides a mock function with given fields: enable
func (_m *MockZoneManager) SetZoomEnable(enable bool) error {
	ret := _m.Called(enable)

	if len(ret) == 0 {
		panic("no return value specified for SetZoomEnable")
	}

	var r0 error
	if rf, ok := ret.Get(0).(func(bool) error); ok {
		r0 = rf(enable)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// Shutdown provides a mock function with given fields: ctx
func (_m *MockZoneManager) Shutdown(ctx context.Context) error {
	ret := _m.Called(ctx)

	if len(ret) == 0 {
		panic("no return value specified for Shutdown")
	}

	var r0 error
	if rf, ok := ret.Get(0).(func(context.Context) error); ok {
		r0 = rf(ctx)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// Start provides a mock function with given fields:
func (_m *MockZoneManager) Start() error {
	ret := _m.Called()

	if len(ret) == 0 {
		panic("no return value specified for Start")
	}

	var r0 error
	if rf, ok := ret.Get(0).(func() error); ok {
		r0 = rf()
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// NewMockZoneManager creates a new instance of MockZoneManager. It also registers a testing interface on the mock and a cleanup function to assert the mocks expectations.
// The first argument is typically a *testing.T value.
func NewMockZoneManager(t interface {
	mock.TestingT
	Cleanup(func())
}) *MockZoneManager {
	mock := &MockZoneManager{}
	mock.Mock.Test(t)

	t.Cleanup(func() { mock.AssertExpectations(t) })

	return mock
}