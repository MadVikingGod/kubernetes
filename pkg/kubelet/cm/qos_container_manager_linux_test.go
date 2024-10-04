//go:build linux
// +build linux

/*
Copyright 2021 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package cm

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func activeTestPods() []*v1.Pod {
	return []*v1.Pod{
		{
			ObjectMeta: metav1.ObjectMeta{
				UID:       "12345678",
				Name:      "guaranteed-pod",
				Namespace: "test",
			},
			Spec: v1.PodSpec{
				Containers: []v1.Container{
					{
						Name:  "foo",
						Image: "busybox",
						Resources: v1.ResourceRequirements{
							Requests: v1.ResourceList{
								v1.ResourceMemory: resource.MustParse("128Mi"),
								v1.ResourceCPU:    resource.MustParse("1"),
							},
							Limits: v1.ResourceList{
								v1.ResourceMemory: resource.MustParse("128Mi"),
								v1.ResourceCPU:    resource.MustParse("1"),
							},
						},
					},
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				UID:       "87654321",
				Name:      "burstable-pod-1",
				Namespace: "test",
			},
			Spec: v1.PodSpec{
				Containers: []v1.Container{
					{
						Name:  "foo",
						Image: "busybox",
						Resources: v1.ResourceRequirements{
							Requests: v1.ResourceList{
								v1.ResourceMemory: resource.MustParse("128Mi"),
								v1.ResourceCPU:    resource.MustParse("1"),
							},
							Limits: v1.ResourceList{
								v1.ResourceMemory: resource.MustParse("256Mi"),
								v1.ResourceCPU:    resource.MustParse("2"),
							},
						},
					},
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				UID:       "01234567",
				Name:      "burstable-pod-2",
				Namespace: "test",
			},
			Spec: v1.PodSpec{
				Containers: []v1.Container{
					{
						Name:  "foo",
						Image: "busybox",
						Resources: v1.ResourceRequirements{
							Requests: v1.ResourceList{
								v1.ResourceMemory: resource.MustParse("256Mi"),
								v1.ResourceCPU:    resource.MustParse("2"),
							},
						},
					},
				},
			},
		},
	}
}

func createTestQOSContainerManager() (*qosContainerManagerImpl, error) {
	subsystems, err := GetCgroupSubsystems()
	if err != nil {
		return nil, fmt.Errorf("failed to get mounted cgroup subsystems: %v", err)
	}

	cgroupRoot := ParseCgroupfsToCgroupName("/")
	cgroupRoot = NewCgroupName(cgroupRoot, defaultNodeAllocatableCgroupName)

	qosContainerManager := &qosContainerManagerImpl{
		subsystems:    subsystems,
		cgroupManager: NewCgroupManager(subsystems, "cgroupfs"),
		cgroupRoot:    cgroupRoot,
		qosReserved:   nil,
	}

	qosContainerManager.activePods = activeTestPods

	return qosContainerManager, nil
}

func TestQoSContainerCgroup(t *testing.T) {
	m, err := createTestQOSContainerManager()
	assert.NoError(t, err)

	qosConfigs := map[v1.PodQOSClass]*CgroupConfig{
		v1.PodQOSGuaranteed: {
			Name:               m.qosContainersInfo.Guaranteed,
			ResourceParameters: &ResourceConfig{},
		},
		v1.PodQOSBurstable: {
			Name:               m.qosContainersInfo.Burstable,
			ResourceParameters: &ResourceConfig{},
		},
		v1.PodQOSBestEffort: {
			Name:               m.qosContainersInfo.BestEffort,
			ResourceParameters: &ResourceConfig{},
		},
	}

	m.setMemoryQoS(qosConfigs)

	burstableMin := resource.MustParse("384Mi")
	guaranteedMin := resource.MustParse("128Mi")
	assert.Equal(t, qosConfigs[v1.PodQOSGuaranteed].ResourceParameters.Unified["memory.min"], strconv.FormatInt(burstableMin.Value()+guaranteedMin.Value(), 10))
	assert.Equal(t, qosConfigs[v1.PodQOSBurstable].ResourceParameters.Unified["memory.min"], strconv.FormatInt(burstableMin.Value(), 10))
}

func Test_qosContainerManagerImpl_UpdateCgroups(t *testing.T) {
	type fields struct {
		// qosContainersInfo  QOSContainersInfo
		// subsystems         *CgroupSubsystems
		cgroupManager CgroupManager
		// getNodeAllocatable func() v1.ResourceList
		// cgroupRoot         CgroupName
		// qosReserved        map[v1.ResourceName]int64
	}
	tests := []struct {
		name      string
		fields    fields
		wantErr   bool
		wantCalls []string
	}{
		{
			name: "Test UpdateCgroups",
			fields: fields{
				cgroupManager: &FakeCgroupManager{},
			},
			wantErr:   false,
			wantCalls: []string{"Update"},
		},
		{
			name: "Test UpdateCgroups with error",
			fields: fields{
				cgroupManager: &FakeCgroupManager{
					update: assert.AnError,
				},
			},
			wantErr:   true,
			wantCalls: []string{"Update"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &qosContainerManagerImpl{
				Mutex: sync.Mutex{},
				// qosContainersInfo:  tt.fields.qosContainersInfo,
				// subsystems:         tt.fields.subsystems,
				cgroupManager: tt.fields.cgroupManager,
				activePods:    activeTestPods,
				// getNodeAllocatable: tt.fields.getNodeAllocatable,
				// cgroupRoot:         tt.fields.cgroupRoot,
				// qosReserved:        tt.fields.qosReserved,
			}
			err := m.UpdateCgroups()
			assert.Equalf(t, tt.wantErr, err != nil, "qosContainerManagerImpl.UpdateCgroups() error = %v, wantErr %v", err, tt.wantErr)

			for _, call := range tt.wantCalls {
				assert.Contains(t, m.cgroupManager.(*FakeCgroupManager).CalledFunctions, call)
			}
		})
	}
}

// This is created by UpdateCgroups, the only caller of setCPUCgroupConfig
func testQosConfigs() map[v1.PodQOSClass]*CgroupConfig {
	return map[v1.PodQOSClass]*CgroupConfig{
		v1.PodQOSGuaranteed: {
			Name:               NewCgroupName(RootCgroupName, "kubelet-test"),
			ResourceParameters: &ResourceConfig{},
		},
		v1.PodQOSBurstable: {
			Name:               NewCgroupName(RootCgroupName, "kubelet-test", strings.ToLower(string(v1.PodQOSBurstable))),
			ResourceParameters: &ResourceConfig{},
		},
		v1.PodQOSBestEffort: {
			Name:               NewCgroupName(RootCgroupName, "kubelet-test", strings.ToLower(string(v1.PodQOSBestEffort))),
			ResourceParameters: &ResourceConfig{},
		},
	}
}

func Test_qosContainerManagerImpl_setCPUCgroupConfig(t *testing.T) {
	qosConfigs := testQosConfigs()

	m := &qosContainerManagerImpl{
		Mutex:      sync.Mutex{},
		activePods: activeTestPods,
	}

	err := m.setCPUCgroupConfig(qosConfigs)
	assert.NoError(t, err, "qosContainerManagerImpl.setCPUCgroupConfig() should not error, error = %v", err)

	assert.Equal(t, *qosConfigs[v1.PodQOSBestEffort].ResourceParameters.CPUShares, uint64(2))
	assert.Greater(t, *qosConfigs[v1.PodQOSBurstable].ResourceParameters.CPUShares, uint64(0))

}

func Test_qosContainerManagerImpl_setHugePagesConfig(t *testing.T) {
	qosConfigs := testQosConfigs()

	m := &qosContainerManagerImpl{
		Mutex:      sync.Mutex{},
		activePods: activeTestPods,
	}

	err := m.setHugePagesConfig(qosConfigs)
	assert.NoError(t, err, "qosContainerManagerImpl.setHugePagesConfig() should not error, error = %v", err)

	for qos, config := range qosConfigs {
		assert.NotNil(t, config.ResourceParameters.HugePageLimit, "qosContainerManagerImpl.setHugePagesConfig() should create the HugePageLimit for %v but did not", qos)
		assert.Greater(t, len(config.ResourceParameters.HugePageLimit), 0, "qosContainerManagerImpl.setHugePagesConfig() should populate the HugePageLimit for %v but did not", qos)
	}
}

func Test_qosContainerManagerImpl_setMemoryReserve(t *testing.T) {
	var i64 = func(i int64) *int64 {
		return &i
	}
	var resourceList = func(mem, cpu string) func() v1.ResourceList {
		return func() v1.ResourceList {
			return v1.ResourceList{
				v1.ResourceMemory: resource.MustParse(mem),
				v1.ResourceCPU:    resource.MustParse(cpu),
			}
		}
	}
	type fields struct {
		getNodeAllocatable func() v1.ResourceList
	}
	type args struct {
		configs        map[v1.PodQOSClass]*CgroupConfig
		percentReserve int64
	}
	tests := []struct {
		name                 string
		fields               fields
		args                 args
		wantBestEffortMemory *int64
		wantBurstableMemory  *int64
	}{
		{
			name: "Test setMemoryReserve",
			args: args{
				configs:        testQosConfigs(),
				percentReserve: 50,
			},
			fields: fields{
				getNodeAllocatable: resourceList("1Gi", "2"),
			},
			wantBurstableMemory:  i64(1006632960), // 1Gi (ResourceMemory) - 50% of 128Mbi (guaranteed)
			wantBestEffortMemory: i64(805306368),  // (BurstLimit) - 50% of 256Mi (burstable)
		},
		{
			name: "Test No Memory Resource",
			args: args{
				configs:        testQosConfigs(),
				percentReserve: 50,
			},
			fields: fields{
				getNodeAllocatable: func() v1.ResourceList {
					return v1.ResourceList{
						v1.ResourceCPU: resource.MustParse("2"),
					}
				},
			},
			wantBestEffortMemory: nil,
			wantBurstableMemory:  nil,
		},
		{
			name: "Test 0 Memory Resource",
			args: args{
				configs:        testQosConfigs(),
				percentReserve: 50,
			},
			fields: fields{
				getNodeAllocatable: resourceList("0", "2"),
			},
			wantBestEffortMemory: nil,
			wantBurstableMemory:  nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &qosContainerManagerImpl{
				Mutex:              sync.Mutex{},
				activePods:         activeTestPods,
				getNodeAllocatable: tt.fields.getNodeAllocatable,
			}
			m.setMemoryReserve(tt.args.configs, tt.args.percentReserve)

			assert.EqualValues(t, tt.wantBestEffortMemory, tt.args.configs[v1.PodQOSBestEffort].ResourceParameters.Memory)
			assert.EqualValues(t, tt.wantBurstableMemory, tt.args.configs[v1.PodQOSBurstable].ResourceParameters.Memory)
		})
	}
}
