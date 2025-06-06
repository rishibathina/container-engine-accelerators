// Copyright 2017 Google Inc. All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package nvidia

import (
	"io/ioutil"
	"os"
	"path"
	"reflect"
	"testing"

	"github.com/GoogleCloudPlatform/container-engine-accelerators/pkg/gpu/nvidia/nvmlutil"
	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/google/go-cmp/cmp"
	pluginapi "k8s.io/kubelet/pkg/apis/deviceplugin/v1beta1"
)

func TestGPUConfig_AddDefaultsAndValidate(t *testing.T) {
	type fields struct {
		GPUPartitionSize           string
		MaxTimeSharedClientsPerGPU int
		GPUSharingConfig           GPUSharingConfig
	}
	tests := []struct {
		name       string
		fields     fields
		wantErr    bool
		wantFields fields
	}{
		{
			name:       "valid config, no sharing",
			fields:     fields{},
			wantErr:    false,
			wantFields: fields{},
		},
		{
			name:    "valid config, time-sharing",
			fields:  fields{MaxTimeSharedClientsPerGPU: 10},
			wantErr: false,
			wantFields: fields{
				MaxTimeSharedClientsPerGPU: 10,
				GPUSharingConfig: GPUSharingConfig{
					GPUSharingStrategy:     "time-sharing",
					MaxSharedClientsPerGPU: 10,
				},
			},
		},
		{
			name: "invalid sharing strategy",
			fields: fields{
				GPUSharingConfig: GPUSharingConfig{
					GPUSharingStrategy:     "invalid",
					MaxSharedClientsPerGPU: 10,
				},
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := &GPUConfig{
				GPUPartitionSize:           tt.fields.GPUPartitionSize,
				MaxTimeSharedClientsPerGPU: tt.fields.MaxTimeSharedClientsPerGPU,
				GPUSharingConfig:           tt.fields.GPUSharingConfig,
			}
			if err := config.AddDefaultsAndValidate(); (err != nil) != tt.wantErr {
				t.Errorf("GPUConfig.AddDefaultsAndValidate() error = %v, wantErr %v", err, tt.wantErr)
			}
			wantConfig := &GPUConfig{
				GPUPartitionSize:           tt.wantFields.GPUPartitionSize,
				MaxTimeSharedClientsPerGPU: tt.wantFields.MaxTimeSharedClientsPerGPU,
				GPUSharingConfig:           tt.wantFields.GPUSharingConfig,
			}
			if !tt.wantErr && !reflect.DeepEqual(config, wantConfig) {
				t.Errorf("GPUConfig was not defaulted correctly, got = %v, want = %v", config, wantConfig)
			}
		})
	}
}

func TestGPUConfig_AddHealthCriticalXid(t *testing.T) {
	type fields struct {
		XID_CONFIG        string
		HealthCriticalXid []int
	}
	tests := []struct {
		name     string
		fields   fields
		wantErr  bool
		wantXids fields
	}{
		{
			name:     "valid config, no HealthCriticalXid",
			fields:   fields{},
			wantErr:  false,
			wantXids: fields{},
		},
		{
			name:    "valid config, HealthCriticalXid",
			fields:  fields{XID_CONFIG: "61, 31"},
			wantErr: false,
			wantXids: fields{
				HealthCriticalXid: []int{61, 31},
			},
		},
		{
			name:    "valid config with empty space HealthCriticalXid",
			fields:  fields{XID_CONFIG: "31,  32,34"},
			wantErr: false,
			wantXids: fields{
				HealthCriticalXid: []int{31, 32, 34},
			},
		},
		{
			name:    "invalid config, HealthCriticalXid",
			fields:  fields{XID_CONFIG: "31,32,x"},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := &GPUConfig{}
			os.Setenv("XID_CONFIG", tt.fields.XID_CONFIG)
			if err := config.AddHealthCriticalXid(); (err != nil) != tt.wantErr {
				t.Errorf("GPUConfig.AddHealthCriticalXid() error = %v, wantErr %v", err, tt.wantErr)
			}
			wantConfig := &GPUConfig{
				HealthCriticalXid: tt.wantXids.HealthCriticalXid,
			}
			if !tt.wantErr && !reflect.DeepEqual(config, wantConfig) {
				t.Errorf("GPUConfig was not defaulted correctly, got = %v, want = %v", config, wantConfig)
			}
			os.Unsetenv("XID_CONFIG")
		})
	}
}

func Test_nvidiaGPUManager_Envs(t *testing.T) {
	tests := []struct {
		name                string
		totalMemPerGPU      uint64
		gpuConfig           GPUConfig
		numDevicesRequested int
		want                map[string]string
	}{
		{
			name:                "No GPU sharing enabled",
			totalMemPerGPU:      80 * 1024,
			gpuConfig:           GPUConfig{},
			numDevicesRequested: 1,
			want:                map[string]string{},
		},
		{
			name:           "time-sharing enabled",
			totalMemPerGPU: 80 * 1024,
			gpuConfig: GPUConfig{
				GPUSharingConfig: GPUSharingConfig{
					GPUSharingStrategy:     "time-sharing",
					MaxSharedClientsPerGPU: 10,
				},
			},
			numDevicesRequested: 1,
			want:                map[string]string{},
		},
		{
			name: "MPS enabled, single GPU request",
			// totalMemPerGPU is 80G.
			totalMemPerGPU: 80 * 1024 * 1024 * 1024,
			gpuConfig: GPUConfig{
				GPUSharingConfig: GPUSharingConfig{
					GPUSharingStrategy:     "mps",
					MaxSharedClientsPerGPU: 10,
				},
			},
			numDevicesRequested: 1,
			want: map[string]string{
				mpsThreadLimitEnv: "10",
				mpsMemLimitEnv:    "0=8192M",
			},
		},
		{
			name: "MPS enabled, multiple GPU request",
			// totalMemPerGPU is 80G.
			totalMemPerGPU: 80 * 1024 * 1024 * 1024,
			gpuConfig: GPUConfig{
				GPUSharingConfig: GPUSharingConfig{
					GPUSharingStrategy:     "mps",
					MaxSharedClientsPerGPU: 10,
				},
			},
			numDevicesRequested: 5,
			want: map[string]string{
				mpsThreadLimitEnv: "50",
				mpsMemLimitEnv:    "0=40960M",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ngm := &nvidiaGPUManager{
				gpuConfig:      tt.gpuConfig,
				totalMemPerGPU: tt.totalMemPerGPU,
			}
			if got := ngm.Envs(tt.numDevicesRequested); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("nvidiaGPUManager.Envs() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_topology(t *testing.T) {
	testDevDir, err := ioutil.TempDir("", "pci")
	defer os.RemoveAll(testDevDir)
	if err != nil {
		t.Errorf("unable to create %q temp dir for testing...", testDevDir)
	}
	device := nvml.Device{}

	testCases := []struct {
		name             string
		pciDevicesRoot   string
		busID            [32]int8
		numaFileContent  string
		numaFileDir      string
		wantTopologyInfo *pluginapi.TopologyInfo
		wantError        bool
	}{
		{
			name:           "valid numa configuration and topology info",
			pciDevicesRoot: testDevDir,
			busID: [32]int8{'0', '0', '0', '0', '0', '0', '0', '0', ':', '3', 'b', ':', '0', '0', '.', '0', 0, // null terminator
				0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
			numaFileContent: "0",
			numaFileDir:     "0000:3b:00.0",
			wantTopologyInfo: &pluginapi.TopologyInfo{
				Nodes: []*pluginapi.NUMANode{
					{
						ID: 0,
					},
				},
			},
			wantError: false,
		},
		{
			name:           "invalid valid numa configuration",
			pciDevicesRoot: testDevDir,
			busID: [32]int8{'0', ':', '3', 'b', ':', '0', '0', '.', '0', 0, // null terminator
				0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
			numaFileContent:  "0",
			numaFileDir:      "0000:3b:00.0",
			wantTopologyInfo: nil,
			wantError:        true,
		},
		{
			name:           "no numa configuration",
			pciDevicesRoot: testDevDir,
			busID: [32]int8{'0', '0', '0', '0', '0', '0', '0', '0', ':', '3', 'b', ':', '0', '0', '.', '0', 0, // null terminator
				0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
			numaFileContent:  "-1",
			numaFileDir:      "0000:3b:00.0",
			wantTopologyInfo: nil,
			wantError:        true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// overriding nvmlutil.NvmlDeviceInfo to nvmlutil.MockDeviceInfo interface
			nvmlutil.NvmlDeviceInfo = &nvmlutil.MockDeviceInfo{}
			pciDevicesRoot = testDevDir
			mockInfo := nvmlutil.NvmlDeviceInfo.(*nvmlutil.MockDeviceInfo)
			mockInfo.BusID = tc.busID
			if len(tc.numaFileContent) > 0 {
				err = os.MkdirAll(path.Join(testDevDir, tc.numaFileDir), 0755)
				if err != nil {
					t.Errorf("unable to create %q directory in %q", tc.numaFileDir, testDevDir)
				}
				fileName := path.Join(testDevDir, "0000:3b:00.0", "numa_node")
				file, err := os.Create(fileName)
				if err != nil {
					t.Errorf("unable to create %q file", fileName)
				}
				err = ioutil.WriteFile(file.Name(), []byte(tc.numaFileContent), 0644)
				if err != nil {
					t.Errorf("unable to write following content to %q file: %q", fileName, tc.numaFileContent)
				}
			}
			gotTopologyInfo, gotError := nvmlutil.Topology(device, tc.pciDevicesRoot)
			if gotError != nil && !tc.wantError {
				t.Errorf("%v", gotError)
			}
			if diff := cmp.Diff(tc.wantTopologyInfo, gotTopologyInfo); diff != "" {
				t.Errorf("unexpected topologyInfo (-want, +got) = %s", diff)
			}

		})
	}
}
