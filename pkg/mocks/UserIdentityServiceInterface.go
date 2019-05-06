// Code generated by mockery v1.0.0. DO NOT EDIT.

package mocks

import mock "github.com/stretchr/testify/mock"
import models "github.com/ProtocolONE/auth1.protocol.one/pkg/models"

// UserIdentityServiceInterface is an autogenerated mock type for the UserIdentityServiceInterface type
type UserIdentityServiceInterface struct {
	mock.Mock
}

// Create provides a mock function with given fields: _a0
func (_m *UserIdentityServiceInterface) Create(_a0 *models.UserIdentity) error {
	ret := _m.Called(_a0)

	var r0 error
	if rf, ok := ret.Get(0).(func(*models.UserIdentity) error); ok {
		r0 = rf(_a0)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// Get provides a mock function with given fields: _a0, _a1, _a2
func (_m *UserIdentityServiceInterface) Get(_a0 *models.Application, _a1 *models.AppIdentityProvider, _a2 string) (*models.UserIdentity, error) {
	ret := _m.Called(_a0, _a1, _a2)

	var r0 *models.UserIdentity
	if rf, ok := ret.Get(0).(func(*models.Application, *models.AppIdentityProvider, string) *models.UserIdentity); ok {
		r0 = rf(_a0, _a1, _a2)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*models.UserIdentity)
		}
	}

	var r1 error
	if rf, ok := ret.Get(1).(func(*models.Application, *models.AppIdentityProvider, string) error); ok {
		r1 = rf(_a0, _a1, _a2)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// Update provides a mock function with given fields: _a0
func (_m *UserIdentityServiceInterface) Update(_a0 *models.UserIdentity) error {
	ret := _m.Called(_a0)

	var r0 error
	if rf, ok := ret.Get(0).(func(*models.UserIdentity) error); ok {
		r0 = rf(_a0)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}