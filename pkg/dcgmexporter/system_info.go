/*
 * Copyright (c) 2021, NVIDIA CORPORATION.  All rights reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package dcgmexporter

import (
	"fmt"
	"math/rand"

	"github.com/NVIDIA/go-dcgm/pkg/dcgm"
	"github.com/sirupsen/logrus"
)

const PARENT_ID_IGNORED = 0

type GroupInfo struct {
	groupHandle dcgm.GroupHandle
	groupType   dcgm.Field_Entity_Group
}

type ComputeInstanceInfo struct {
	InstanceInfo dcgm.MigEntityInfo
	ProfileName  string
	EntityId     uint
}

type GpuInstanceInfo struct {
	Info             dcgm.MigEntityInfo
	ProfileName      string
	EntityId         uint
	ComputeInstances []ComputeInstanceInfo
}

type GpuInfo struct {
	DeviceInfo   dcgm.Device
	GpuInstances []GpuInstanceInfo
	MigEnabled   bool
}

type SwitchInfo struct {
	EntityId uint
	NvLinks  []dcgm.NvLinkStatus
}

type SystemInfo struct {
	GpuCount uint
	Gpus     [dcgm.MAX_NUM_DEVICES]GpuInfo
	gOpt     DeviceOptions
	sOpt     DeviceOptions
	InfoType dcgm.Field_Entity_Group
	Switches []SwitchInfo
}

type MonitoringInfo struct {
	Entity       dcgm.GroupEntityPair
	DeviceInfo   dcgm.Device
	InstanceInfo *GpuInstanceInfo
	ParentId     uint
}

func SetGpuInstanceProfileName(sysInfo *SystemInfo, entityId uint, profileName string) bool {
	for i := uint(0); i < sysInfo.GpuCount; i++ {
		for j := range sysInfo.Gpus[i].GpuInstances {
			if sysInfo.Gpus[i].GpuInstances[j].EntityId == entityId {
				sysInfo.Gpus[i].GpuInstances[j].ProfileName = profileName
				return true
			}
		}
	}

	return false
}

func SetMigProfileNames(sysInfo *SystemInfo, values []dcgm.FieldValue_v2) error {
	notFound := false
	err := fmt.Errorf("Cannot find match for entities:")
	for _, v := range values {
		found := SetGpuInstanceProfileName(sysInfo, v.EntityId, dcgm.Fv2_String(v))
		if found == false {
			err = fmt.Errorf("%s group %d, id %d", err, v.EntityGroupId, v.EntityId)
			notFound = true
		}
	}

	if notFound {
		return err
	}

	return nil
}

func PopulateMigProfileNames(sysInfo *SystemInfo, entities []dcgm.GroupEntityPair) error {
	if len(entities) == 0 {
		// There are no entities to populate
		return nil
	}

	var fields []dcgm.Short
	fields = append(fields, dcgm.DCGM_FI_DEV_NAME)
	flags := dcgm.DCGM_FV_FLAG_LIVE_DATA
	values, err := dcgm.EntitiesGetLatestValues(entities, fields, flags)

	if err != nil {
		return err
	}

	return SetMigProfileNames(sysInfo, values)
}

func GpuIdExists(sysInfo *SystemInfo, gpuId int) bool {
	for i := uint(0); i < sysInfo.GpuCount; i++ {
		if sysInfo.Gpus[i].DeviceInfo.GPU == uint(gpuId) {
			return true
		}
	}
	return false
}

func SwitchIdExists(sysInfo *SystemInfo, switchId int) bool {
	for _, sw := range sysInfo.Switches {
		if sw.EntityId == uint(switchId) {
			return true
		}
	}
	return false
}

func GpuInstanceIdExists(sysInfo *SystemInfo, gpuInstanceId int) bool {
	for i := uint(0); i < sysInfo.GpuCount; i++ {
		for _, instance := range sysInfo.Gpus[i].GpuInstances {
			if instance.EntityId == uint(gpuInstanceId) {
				return true
			}
		}
	}
	return false
}

func LinkIdExists(sysInfo *SystemInfo, linkId int) bool {
	for _, sw := range sysInfo.Switches {
		for _, link := range sw.NvLinks {
			if link.Index == uint(linkId) {
				return true
			}
		}
	}
	return false
}

func VerifySwitchDevicePresence(sysInfo *SystemInfo, sOpt DeviceOptions) error {
	if sOpt.Flex {
		return nil
	}

	if len(sOpt.MajorRange) > 0 && sOpt.MajorRange[0] != -1 {
		// Verify we can find all the specified Switches
		for _, swId := range sOpt.MajorRange {
			if !SwitchIdExists(sysInfo, swId) {
				return fmt.Errorf("couldn't find requested NvSwitch id %d", swId)
			}
		}
	}

	if len(sOpt.MinorRange) > 0 && sOpt.MinorRange[0] != -1 {
		for _, linkId := range sOpt.MinorRange {
			if !LinkIdExists(sysInfo, linkId) {
				return fmt.Errorf("couldn't find requested NvLink %d", linkId)
			}
		}
	}

	return nil
}

func VerifyDevicePresence(sysInfo *SystemInfo, gOpt DeviceOptions) error {
	if gOpt.Flex {
		return nil
	}

	if len(gOpt.MajorRange) > 0 && gOpt.MajorRange[0] != -1 {
		// Verify we can find all the specified GPUs
		for _, gpuId := range gOpt.MajorRange {
			if GpuIdExists(sysInfo, gpuId) == false {
				return fmt.Errorf("Couldn't find requested GPU id %d", gpuId)
			}
		}
	}

	if len(gOpt.MinorRange) > 0 && gOpt.MinorRange[0] != -1 {
		for _, gpuInstanceId := range gOpt.MinorRange {
			if GpuInstanceIdExists(sysInfo, gpuInstanceId) == false {
				return fmt.Errorf("Couldn't find requested GPU instance id %d", gpuInstanceId)
			}
		}
	}

	return nil
}

func InitializeNvSwitchInfo(sysInfo SystemInfo, sOpt DeviceOptions) (SystemInfo, error) {
	switches, err := dcgm.GetEntityGroupEntities(dcgm.FE_SWITCH)
	if err != nil {
		return sysInfo, err
	}

	if len(switches) <= 0 {
		return sysInfo, fmt.Errorf("no switches to monitor")
	}

	links, err := dcgm.GetNvLinkLinkStatus()
	if err != nil {
		return sysInfo, err
	}

	for i := 0; i < len(switches); i++ {
		var matchingLinks []dcgm.NvLinkStatus
		for _, link := range links {
			if link.ParentType == dcgm.FE_SWITCH && link.ParentId == uint(switches[i]) {
				matchingLinks = append(matchingLinks, link)
			}
		}

		sw := SwitchInfo{
			switches[i],
			matchingLinks,
		}

		sysInfo.Switches = append(sysInfo.Switches, sw)
	}

	sysInfo.sOpt = sOpt
	err = VerifySwitchDevicePresence(&sysInfo, sOpt)

	return sysInfo, nil
}

func InitializeGpuInfo(sysInfo SystemInfo, gOpt DeviceOptions, useFakeGpus bool) (SystemInfo, error) {
	gpuCount, err := dcgm.GetAllDeviceCount()
	if err != nil {
		return sysInfo, err
	}
	sysInfo.GpuCount = gpuCount

	for i := uint(0); i < sysInfo.GpuCount; i++ {
		// Default mig enabled to false
		sysInfo.Gpus[i].MigEnabled = false
		sysInfo.Gpus[i].DeviceInfo, err = dcgm.GetDeviceInfo(i)
		if err != nil {
			if useFakeGpus {
				sysInfo.Gpus[i].DeviceInfo.GPU = i
				sysInfo.Gpus[i].DeviceInfo.UUID = fmt.Sprintf("fake%d", i)
			} else {
				return sysInfo, err
			}
		}
	}

	hierarchy, err := dcgm.GetGpuInstanceHierarchy()
	if err != nil {
		return sysInfo, err
	}

	if hierarchy.Count > 0 {
		var entities []dcgm.GroupEntityPair

		gpuId := uint(0)
		instanceIndex := 0
		for i := uint(0); i < hierarchy.Count; i++ {
			if hierarchy.EntityList[i].Parent.EntityGroupId == dcgm.FE_GPU {
				// We are adding a GPU instance
				gpuId = hierarchy.EntityList[i].Parent.EntityId
				entityId := hierarchy.EntityList[i].Entity.EntityId
				instanceInfo := GpuInstanceInfo{
					Info:        hierarchy.EntityList[i].Info,
					ProfileName: "",
					EntityId:    entityId,
				}
				sysInfo.Gpus[gpuId].MigEnabled = true
				sysInfo.Gpus[gpuId].GpuInstances = append(sysInfo.Gpus[gpuId].GpuInstances, instanceInfo)
				entities = append(entities, dcgm.GroupEntityPair{dcgm.FE_GPU_I, entityId})
				instanceIndex = len(sysInfo.Gpus[gpuId].GpuInstances) - 1
			} else if hierarchy.EntityList[i].Parent.EntityGroupId == dcgm.FE_GPU_I {
				// Add the compute instance, gpuId is recorded previously
				entityId := hierarchy.EntityList[i].Entity.EntityId
				ciInfo := ComputeInstanceInfo{hierarchy.EntityList[i].Info, "", entityId}
				sysInfo.Gpus[gpuId].GpuInstances[instanceIndex].ComputeInstances = append(sysInfo.Gpus[gpuId].GpuInstances[instanceIndex].ComputeInstances, ciInfo)
			}
		}

		err = PopulateMigProfileNames(&sysInfo, entities)
		if err != nil {
			return sysInfo, err
		}
	}

	sysInfo.gOpt = gOpt
	err = VerifyDevicePresence(&sysInfo, gOpt)

	return sysInfo, nil
}

func InitializeSystemInfo(gOpt DeviceOptions, sOpt DeviceOptions, useFakeGpus bool, entityType dcgm.Field_Entity_Group) (SystemInfo, error) {
	sysInfo := SystemInfo{}

	logrus.Info("Initializing system entities of type: ", entityType)
	switch entityType {
	case dcgm.FE_LINK:
		sysInfo.InfoType = dcgm.FE_LINK
		return InitializeNvSwitchInfo(sysInfo, sOpt)
	case dcgm.FE_SWITCH:
		sysInfo.InfoType = dcgm.FE_SWITCH
		return InitializeNvSwitchInfo(sysInfo, sOpt)
	case dcgm.FE_GPU:
		sysInfo.InfoType = dcgm.FE_GPU
		return InitializeGpuInfo(sysInfo, gOpt, useFakeGpus)
	}

	return sysInfo, fmt.Errorf("unhandled entity type: %d", entityType)
}

func CreateLinkGroupsFromSystemInfo(sysInfo SystemInfo) ([]dcgm.GroupHandle, []func(), error) {
	var groups []dcgm.GroupHandle
	var cleanups []func()

	/* Create per-switch link groups */
	for _, sw := range sysInfo.Switches {
		if !IsSwitchWatched(sw.EntityId, sysInfo) {
			continue
		}

		groupId, err := dcgm.CreateGroup(fmt.Sprintf("gpu-collector-group-%d", rand.Uint64()))
		if err != nil {
			return nil, cleanups, err
		}

		groups = append(groups, groupId)

		for _, link := range sw.NvLinks {
			if link.State != dcgm.LS_UP {
				continue
			}

			if !IsLinkWatched(link.Index, sw.EntityId, sysInfo) {
				continue
			}

			err = dcgm.AddLinkEntityToGroup(groupId, link.Index, link.ParentId)

			if err != nil {
				return groups, cleanups, err
			}

			cleanups = append(cleanups, func() { dcgm.DestroyGroup(groupId) })
		}
	}

	return groups, cleanups, nil
}

func CreateGroupFromSystemInfo(sysInfo SystemInfo) (dcgm.GroupHandle, func(), error) {
	monitoringInfo := GetMonitoredEntities(sysInfo)
	groupId, err := dcgm.CreateGroup(fmt.Sprintf("gpu-collector-group-%d", rand.Uint64()))
	if err != nil {
		return dcgm.GroupHandle{}, func() {}, err
	}

	for _, mi := range monitoringInfo {
		err := dcgm.AddEntityToGroup(groupId, mi.Entity.EntityGroupId, mi.Entity.EntityId)
		if err != nil {
			return groupId, func() { dcgm.DestroyGroup(groupId) }, err
		}
	}

	return groupId, func() { dcgm.DestroyGroup(groupId) }, nil
}

func AddAllGpus(sysInfo SystemInfo) []MonitoringInfo {
	var monitoring []MonitoringInfo

	for i := uint(0); i < sysInfo.GpuCount; i++ {
		mi := MonitoringInfo{
			dcgm.GroupEntityPair{dcgm.FE_GPU, sysInfo.Gpus[i].DeviceInfo.GPU},
			sysInfo.Gpus[i].DeviceInfo,
			nil,
			PARENT_ID_IGNORED,
		}
		monitoring = append(monitoring, mi)
	}

	return monitoring
}

func AddAllSwitches(sysInfo SystemInfo) []MonitoringInfo {
	var monitoring []MonitoringInfo

	for _, sw := range sysInfo.Switches {
		if !IsSwitchWatched(sw.EntityId, sysInfo) {
			continue
		}

		mi := MonitoringInfo{
			dcgm.GroupEntityPair{dcgm.FE_SWITCH, sw.EntityId},
			dcgm.Device{
				0, "", "", 0,
				dcgm.PCIInfo{"", 0, 0, 0},
				dcgm.DeviceIdentifiers{"", "", "", "", "", ""},
				nil, "",
			},
			nil,
			PARENT_ID_IGNORED,
		}
		monitoring = append(monitoring, mi)
	}

	return monitoring
}

func AddAllLinks(sysInfo SystemInfo) []MonitoringInfo {
	var monitoring []MonitoringInfo

	for _, sw := range sysInfo.Switches {
		for _, link := range sw.NvLinks {
			if link.State != dcgm.LS_UP {
				continue
			}

			if !IsSwitchWatched(sw.EntityId, sysInfo) {
				continue
			}

			if !IsLinkWatched(link.Index, sw.EntityId, sysInfo) {
				continue
			}

			mi := MonitoringInfo{
				dcgm.GroupEntityPair{dcgm.FE_LINK, link.Index},
				dcgm.Device{
					0, "", "", 0,
					dcgm.PCIInfo{"", 0, 0, 0},
					dcgm.DeviceIdentifiers{"", "", "", "", "", ""},
					nil, "",
				},
				nil,
				link.ParentId,
			}
			monitoring = append(monitoring, mi)
		}
	}

	return monitoring
}

func IsSwitchWatched(switchId uint, sysInfo SystemInfo) bool {
	if sysInfo.sOpt.Flex {
		return true
	}

	if len(sysInfo.sOpt.MajorRange) <= 0 {
		return true
	}

	for _, sw := range sysInfo.sOpt.MajorRange {
		if uint(sw) == switchId {
			return true
		}

	}
	return false
}

func IsLinkWatched(linkId uint, switchId uint, sysInfo SystemInfo) bool {
	if sysInfo.sOpt.Flex {
		return true
	}

	for _, sw := range sysInfo.Switches {
		if !IsSwitchWatched(sw.EntityId, sysInfo) {
			return false
		}

		if len(sysInfo.sOpt.MinorRange) <= 0 {
			return true
		}

		for _, link := range sysInfo.sOpt.MinorRange {
			if uint(link) == linkId {
				return true
			}
		}
		return false
	}

	return false
}

func AddAllGpuInstances(sysInfo SystemInfo, addFlexibly bool) []MonitoringInfo {
	var monitoring []MonitoringInfo

	for i := uint(0); i < sysInfo.GpuCount; i++ {
		if addFlexibly == true && len(sysInfo.Gpus[i].GpuInstances) == 0 {
			mi := MonitoringInfo{
				dcgm.GroupEntityPair{dcgm.FE_GPU, sysInfo.Gpus[i].DeviceInfo.GPU},
				sysInfo.Gpus[i].DeviceInfo,
				nil,
				PARENT_ID_IGNORED,
			}
			monitoring = append(monitoring, mi)
		} else {
			for j := 0; j < len(sysInfo.Gpus[i].GpuInstances); j++ {
				mi := MonitoringInfo{
					dcgm.GroupEntityPair{dcgm.FE_GPU_I, sysInfo.Gpus[i].GpuInstances[j].EntityId},
					sysInfo.Gpus[i].DeviceInfo,
					&sysInfo.Gpus[i].GpuInstances[j],
					PARENT_ID_IGNORED,
				}
				monitoring = append(monitoring, mi)
			}
		}
	}

	return monitoring
}

func GetMonitoringInfoForGpu(sysInfo SystemInfo, gpuId int) *MonitoringInfo {
	for i := uint(0); i < sysInfo.GpuCount; i++ {
		if sysInfo.Gpus[i].DeviceInfo.GPU == uint(gpuId) {
			return &MonitoringInfo{
				dcgm.GroupEntityPair{dcgm.FE_GPU, sysInfo.Gpus[i].DeviceInfo.GPU},
				sysInfo.Gpus[i].DeviceInfo,
				nil,
				PARENT_ID_IGNORED,
			}
		}
	}

	return nil
}

func GetMonitoringInfoForGpuInstance(sysInfo SystemInfo, gpuInstanceId int) *MonitoringInfo {
	for i := uint(0); i < sysInfo.GpuCount; i++ {
		for _, instance := range sysInfo.Gpus[i].GpuInstances {
			if instance.EntityId == uint(gpuInstanceId) {
				return &MonitoringInfo{
					dcgm.GroupEntityPair{dcgm.FE_GPU_I, uint(gpuInstanceId)},
					sysInfo.Gpus[i].DeviceInfo,
					&instance,
					PARENT_ID_IGNORED,
				}
			}
		}
	}

	return nil
}

func GetMonitoredEntities(sysInfo SystemInfo) []MonitoringInfo {
	var monitoring []MonitoringInfo

	if sysInfo.InfoType == dcgm.FE_SWITCH {
		monitoring = AddAllSwitches(sysInfo)
	} else if sysInfo.InfoType == dcgm.FE_LINK {
		monitoring = AddAllLinks(sysInfo)
	} else if sysInfo.gOpt.Flex == true {
		monitoring = AddAllGpuInstances(sysInfo, true)
	} else {
		if len(sysInfo.gOpt.MajorRange) > 0 && sysInfo.gOpt.MajorRange[0] == -1 {
			monitoring = AddAllGpus(sysInfo)
		} else {
			for _, gpuId := range sysInfo.gOpt.MajorRange {
				// We've already verified that everything in the options list exists
				monitoring = append(monitoring, *GetMonitoringInfoForGpu(sysInfo, gpuId))
			}
		}

		if len(sysInfo.gOpt.MinorRange) > 0 && sysInfo.gOpt.MinorRange[0] == -1 {
			monitoring = AddAllGpuInstances(sysInfo, false)
		} else {
			for _, gpuInstanceId := range sysInfo.gOpt.MinorRange {
				// We've already verified that everything in the options list exists
				monitoring = append(monitoring, *GetMonitoringInfoForGpuInstance(sysInfo, gpuInstanceId))
			}
		}
	}

	return monitoring
}

func GetGpuInstanceIdentifier(sysInfo SystemInfo, gpuuuid string, gpuInstanceId uint) string {
	for i := uint(0); i < sysInfo.GpuCount; i++ {
		if sysInfo.Gpus[i].DeviceInfo.UUID == gpuuuid {
			identifier := fmt.Sprintf("%d-%d", sysInfo.Gpus[i].DeviceInfo.GPU, gpuInstanceId)
			return identifier
		}
	}

	return ""
}
