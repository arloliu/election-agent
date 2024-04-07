// Code generated by mockery v2.26.1. DO NOT EDIT.

package redlock

import (
	context "context"
	time "time"

	mock "github.com/stretchr/testify/mock"
)

// MockConn is an autogenerated mock type for the Conn type
type MockConn struct {
	mock.Mock
}

// Close provides a mock function with given fields: ctx
func (_m *MockConn) Close(ctx context.Context) error {
	ret := _m.Called(ctx)

	var r0 error
	if rf, ok := ret.Get(0).(func(context.Context) error); ok {
		r0 = rf(ctx)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// Eval provides a mock function with given fields: script, keysAndArgs
func (_m *MockConn) Eval(script *Script, keysAndArgs ...interface{}) (interface{}, error) {
	ret := _m.Called(script, keysAndArgs)

	var r0 interface{}
	var r1 error
	if rf, ok := ret.Get(0).(func(*Script, ...interface{}) (interface{}, error)); ok {
		return rf(script, keysAndArgs...)
	}
	if rf, ok := ret.Get(0).(func(*Script, ...interface{}) interface{}); ok {
		r0 = rf(script, keysAndArgs...)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(interface{})
		}
	}

	if rf, ok := ret.Get(1).(func(*Script, ...interface{}) error); ok {
		r1 = rf(script, keysAndArgs...)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// Get provides a mock function with given fields: name
func (_m *MockConn) Get(name string) (string, error) {
	ret := _m.Called(name)

	var r0 string
	var r1 error
	if rf, ok := ret.Get(0).(func(string) (string, error)); ok {
		return rf(name)
	}
	if rf, ok := ret.Get(0).(func(string) string); ok {
		r0 = rf(name)
	} else {
		r0 = ret.Get(0).(string)
	}

	if rf, ok := ret.Get(1).(func(string) error); ok {
		r1 = rf(name)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// MGet provides a mock function with given fields: keys
func (_m *MockConn) MGet(keys ...string) ([]string, error) {
	ret := _m.Called(keys)

	var r0 []string
	var r1 error
	if rf, ok := ret.Get(0).(func(...string) ([]string, error)); ok {
		return rf(keys...)
	}
	if rf, ok := ret.Get(0).(func(...string) []string); ok {
		r0 = rf(keys...)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).([]string)
		}
	}

	if rf, ok := ret.Get(1).(func(...string) error); ok {
		r1 = rf(keys...)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// MSet provides a mock function with given fields: pairs
func (_m *MockConn) MSet(pairs ...interface{}) (bool, error) {
	ret := _m.Called(pairs)

	var r0 bool
	var r1 error
	if rf, ok := ret.Get(0).(func(...interface{}) (bool, error)); ok {
		return rf(pairs...)
	}
	if rf, ok := ret.Get(0).(func(...interface{}) bool); ok {
		r0 = rf(pairs...)
	} else {
		r0 = ret.Get(0).(bool)
	}

	if rf, ok := ret.Get(1).(func(...interface{}) error); ok {
		r1 = rf(pairs...)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// PTTL provides a mock function with given fields: name
func (_m *MockConn) PTTL(name string) (time.Duration, error) {
	ret := _m.Called(name)

	var r0 time.Duration
	var r1 error
	if rf, ok := ret.Get(0).(func(string) (time.Duration, error)); ok {
		return rf(name)
	}
	if rf, ok := ret.Get(0).(func(string) time.Duration); ok {
		r0 = rf(name)
	} else {
		r0 = ret.Get(0).(time.Duration)
	}

	if rf, ok := ret.Get(1).(func(string) error); ok {
		r1 = rf(name)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// Ping provides a mock function with given fields:
func (_m *MockConn) Ping() (bool, error) {
	ret := _m.Called()

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

// Set provides a mock function with given fields: name, value
func (_m *MockConn) Set(name string, value string) (bool, error) {
	ret := _m.Called(name, value)

	var r0 bool
	var r1 error
	if rf, ok := ret.Get(0).(func(string, string) (bool, error)); ok {
		return rf(name, value)
	}
	if rf, ok := ret.Get(0).(func(string, string) bool); ok {
		r0 = rf(name, value)
	} else {
		r0 = ret.Get(0).(bool)
	}

	if rf, ok := ret.Get(1).(func(string, string) error); ok {
		r1 = rf(name, value)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// SetNX provides a mock function with given fields: name, value, expiry
func (_m *MockConn) SetNX(name string, value string, expiry time.Duration) (bool, error) {
	ret := _m.Called(name, value, expiry)

	var r0 bool
	var r1 error
	if rf, ok := ret.Get(0).(func(string, string, time.Duration) (bool, error)); ok {
		return rf(name, value, expiry)
	}
	if rf, ok := ret.Get(0).(func(string, string, time.Duration) bool); ok {
		r0 = rf(name, value, expiry)
	} else {
		r0 = ret.Get(0).(bool)
	}

	if rf, ok := ret.Get(1).(func(string, string, time.Duration) error); ok {
		r1 = rf(name, value, expiry)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// WithContext provides a mock function with given fields: ctx
func (_m *MockConn) WithContext(ctx context.Context) Conn {
	ret := _m.Called(ctx)

	var r0 Conn
	if rf, ok := ret.Get(0).(func(context.Context) Conn); ok {
		r0 = rf(ctx)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(Conn)
		}
	}

	return r0
}

type mockConstructorTestingTNewMockConn interface {
	mock.TestingT
	Cleanup(func())
}

// NewMockConn creates a new instance of MockConn. It also registers a testing interface on the mock and a cleanup function to assert the mocks expectations.
func NewMockConn(t mockConstructorTestingTNewMockConn) *MockConn {
	mock := &MockConn{}
	mock.Mock.Test(t)

	t.Cleanup(func() { mock.AssertExpectations(t) })

	return mock
}
