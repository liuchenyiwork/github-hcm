/*
 * TencentBlueKing is pleased to support the open source community by making
 * 蓝鲸智云 - 混合云管理平台 (BlueKing - Hybrid Cloud Management System) available.
 * Copyright (C) 2022 THL A29 Limited,
 * a Tencent company. All rights reserved.
 * Licensed under the MIT License (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at http://opensource.org/licenses/MIT
 * Unless required by applicable law or agreed to in writing,
 * software distributed under the License is distributed on
 * an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND,
 * either express or implied. See the License for the
 * specific language governing permissions and limitations under the License.
 *
 * We undertake not to change the open source license (MIT license) applicable
 *
 * to the current version of the project delivered to anyone in the future.
 */

package gcp

import (
	"hcm/cmd/hc-service/logics/sync/cvm"
	syncdisk "hcm/cmd/hc-service/logics/sync/disk"
	cloudclient "hcm/cmd/hc-service/service/cloud-adaptor"
	"hcm/cmd/hc-service/service/disk/datasvc"
	"hcm/pkg/adaptor/types/disk"
	"hcm/pkg/api/core"
	dataproto "hcm/pkg/api/data-service/cloud/disk"
	proto "hcm/pkg/api/hc-service/disk"
	dataservice "hcm/pkg/client/data-service"
	"hcm/pkg/criteria/enumor"
	"hcm/pkg/criteria/errf"
	"hcm/pkg/kit"
	"hcm/pkg/logs"
	"hcm/pkg/rest"
	"hcm/pkg/runtime/filter"
)

// DiskSvc ...
type DiskSvc struct {
	Adaptor *cloudclient.CloudAdaptorClient
	DataCli *dataservice.Client
}

// CreateDisk ...
func (svc *DiskSvc) CreateDisk(cts *rest.Contexts) (interface{}, error) {
	req := new(proto.GcpDiskCreateReq)
	if err := cts.DecodeInto(req); err != nil {
		return nil, errf.NewFromErr(errf.DecodeRequestFailed, err)
	}
	if err := req.Validate(); err != nil {
		return nil, errf.NewFromErr(errf.InvalidParameter, err)
	}

	client, err := svc.Adaptor.Gcp(cts.Kit, req.AccountID)
	if err != nil {
		return nil, err
	}

	diskSize := int64(req.DiskSize)
	opt := &disk.GcpDiskCreateOption{
		DiskName: *req.DiskName,
		Region:   req.Region,
		Zone:     req.Zone,
		DiskType: req.DiskType,
		DiskSize: diskSize,
	}
	result, err := client.CreateDisk(cts.Kit, opt)
	if err != nil {
		return nil, err
	}

	if len(result.UnknownCloudIDs) > 0 {
		logs.Errorf("disk(%v) is unknown, rid: %s", result.UnknownCloudIDs, cts.Kit.Rid)
	}

	cloudIDs := result.SuccessCloudIDs

	_, err = syncdisk.SyncGcpDisk(
		cts.Kit,
		&syncdisk.SyncGcpDiskOption{
			AccountID: req.AccountID,
			Zone:      req.Zone,
			CloudIDs:  cloudIDs,
		},
		svc.Adaptor,
		svc.DataCli,
	)
	if err != nil {
		return nil, err
	}

	resp, err := svc.DataCli.Global.ListDisk(
		cts.Kit.Ctx,
		cts.Kit.Header(),
		&dataproto.DiskListReq{Filter: &filter.Expression{
			Op: filter.And,
			Rules: []filter.RuleFactory{
				&filter.AtomRule{
					Field: "cloud_id",
					Op:    filter.In.Factory(),
					Value: cloudIDs,
				}, &filter.AtomRule{
					Field: "vendor",
					Op:    filter.Equal.Factory(),
					Value: string(enumor.Gcp),
				},
			},
		}, Page: &core.BasePage{Limit: uint(len(cloudIDs))}, Fields: []string{"id"}},
	)

	diskIDs := make([]string, len(cloudIDs))
	for idx, diskData := range resp.Details {
		diskIDs[idx] = diskData.ID
	}
	return &core.BatchCreateResult{IDs: diskIDs}, nil
}

// DeleteDisk ...
func (svc *DiskSvc) DeleteDisk(cts *rest.Contexts) (interface{}, error) {
	req := new(proto.DiskDeleteReq)
	if err := cts.DecodeInto(req); err != nil {
		return nil, errf.NewFromErr(errf.DecodeRequestFailed, err)
	}
	if err := req.Validate(); err != nil {
		return nil, errf.NewFromErr(errf.InvalidParameter, err)
	}

	opt, err := svc.makeDiskDeleteOption(cts.Kit, req)
	if err != nil {
		return nil, err
	}

	client, err := svc.Adaptor.Gcp(cts.Kit, req.AccountID)
	if err != nil {
		return nil, err
	}

	err = client.DeleteDisk(cts.Kit, opt)
	if err != nil {
		return nil, err
	}

	manager := datasvc.DiskManager{DataCli: svc.DataCli}
	return nil, manager.Delete(cts.Kit, []string{req.DiskID})
}

// AttachDisk ...
func (svc *DiskSvc) AttachDisk(cts *rest.Contexts) (interface{}, error) {
	req := new(proto.GcpDiskAttachReq)
	if err := cts.DecodeInto(req); err != nil {
		return nil, errf.NewFromErr(errf.DecodeRequestFailed, err)
	}
	if err := req.Validate(); err != nil {
		return nil, errf.NewFromErr(errf.InvalidParameter, err)
	}

	opt, err := svc.makeDiskAttachOption(cts.Kit, req)
	if err != nil {
		return nil, err
	}

	client, err := svc.Adaptor.Gcp(cts.Kit, req.AccountID)
	if err != nil {
		return nil, err
	}

	err = client.AttachDisk(cts.Kit, opt)
	if err != nil {
		return nil, err
	}

	manager := datasvc.DiskCvmRelManager{CvmID: req.CvmID, DiskID: req.DiskID, DataCli: svc.DataCli}
	err = manager.Create(cts.Kit)
	if err != nil {
		return nil, err
	}

	diskData, err := svc.DataCli.Gcp.RetrieveDisk(cts.Kit.Ctx, cts.Kit.Header(), req.DiskID)
	if err != nil {
		return nil, err
	}
	_, err = syncdisk.SyncGcpDisk(
		cts.Kit,
		&syncdisk.SyncGcpDiskOption{
			AccountID: req.AccountID,
			Zone:      opt.Zone,
			CloudIDs:  []string{diskData.CloudID},
		},
		svc.Adaptor,
		svc.DataCli,
	)
	if err != nil {
		logs.Errorf("SyncGcpDisk failed, err: %v, rid: %s", err, cts.Kit.Rid)
		return nil, err
	}

	cvmData, err := svc.DataCli.Gcp.Cvm.GetCvm(cts.Kit.Ctx, cts.Kit.Header(), req.CvmID)
	if err != nil {
		return nil, err
	}

	return cvm.SyncGcpCvm(
		cts.Kit,
		svc.Adaptor,
		svc.DataCli,
		&cvm.SyncGcpCvmOption{
			AccountID: req.AccountID,
			Region:    cvmData.Region,
			CloudIDs:  []string{cvmData.CloudID},
			Zone:      opt.Zone,
		},
	)
}

// DetachDisk ...
func (svc *DiskSvc) DetachDisk(cts *rest.Contexts) (interface{}, error) {
	req := new(proto.DiskDetachReq)
	if err := cts.DecodeInto(req); err != nil {
		return nil, errf.NewFromErr(errf.DecodeRequestFailed, err)
	}
	if err := req.Validate(); err != nil {
		return nil, errf.NewFromErr(errf.InvalidParameter, err)
	}

	opt, err := svc.makeDiskDetachOption(cts.Kit, req)
	if err != nil {
		return nil, err
	}

	client, err := svc.Adaptor.Gcp(cts.Kit, req.AccountID)
	if err != nil {
		return nil, err
	}

	err = client.DetachDisk(cts.Kit, opt)
	if err != nil {
		return nil, err
	}

	manager := datasvc.DiskCvmRelManager{CvmID: req.CvmID, DiskID: req.DiskID, DataCli: svc.DataCli}
	err = manager.Delete(cts.Kit)
	if err != nil {
		return nil, err
	}

	diskData, err := svc.DataCli.Gcp.RetrieveDisk(cts.Kit.Ctx, cts.Kit.Header(), req.DiskID)
	if err != nil {
		return nil, err
	}

	_, err = syncdisk.SyncGcpDisk(
		cts.Kit,
		&syncdisk.SyncGcpDiskOption{
			AccountID: req.AccountID,
			Zone:      opt.Zone,
			CloudIDs:  []string{diskData.CloudID},
		},
		svc.Adaptor,
		svc.DataCli,
	)
	if err != nil {
		logs.Errorf("SyncGcpDisk failed, err: %v, rid: %s", err, cts.Kit.Rid)
		return nil, err
	}

	cvmData, err := svc.DataCli.Gcp.Cvm.GetCvm(cts.Kit.Ctx, cts.Kit.Header(), req.CvmID)
	if err != nil {
		return nil, err
	}

	return cvm.SyncGcpCvm(
		cts.Kit,
		svc.Adaptor,
		svc.DataCli,
		&cvm.SyncGcpCvmOption{
			AccountID: req.AccountID,
			Zone:      opt.Zone,
			Region:    cvmData.Region,
			CloudIDs:  []string{cvmData.CloudID},
		},
	)
}

func (svc *DiskSvc) makeDiskAttachOption(
	kt *kit.Kit,
	req *proto.GcpDiskAttachReq,
) (*disk.GcpDiskAttachOption, error) {
	dataCli := svc.DataCli.Gcp

	diskData, err := dataCli.RetrieveDisk(kt.Ctx, kt.Header(), req.DiskID)
	if err != nil {
		return nil, err
	}

	cvmData, err := dataCli.Cvm.GetCvm(kt.Ctx, kt.Header(), req.CvmID)
	if err != nil {
		return nil, err
	}

	return &disk.GcpDiskAttachOption{
		Zone:       diskData.Zone,
		CvmName:    cvmData.Name,
		DiskName:   diskData.Name,
		DeviceName: req.DeviceName,
	}, nil
}

func (svc *DiskSvc) makeDiskDetachOption(
	kt *kit.Kit,
	req *proto.DiskDetachReq,
) (*disk.GcpDiskDetachOption, error) {
	dataCli := svc.DataCli.Gcp

	diskData, err := dataCli.RetrieveDisk(kt.Ctx, kt.Header(), req.DiskID)
	if err != nil {
		return nil, err
	}

	cvmData, err := dataCli.Cvm.GetCvm(kt.Ctx, kt.Header(), req.CvmID)
	if err != nil {
		return nil, err
	}

	var deviceName string
	for _, d := range cvmData.Extension.Disks {
		if d.CloudID == diskData.CloudID {
			deviceName = d.DeviceName
			break
		}
	}

	return &disk.GcpDiskDetachOption{
		Zone:       diskData.Zone,
		CvmName:    cvmData.Name,
		DeviceName: deviceName,
		DiskName:   diskData.Name,
	}, nil
}

func (svc *DiskSvc) makeDiskDeleteOption(
	kt *kit.Kit,
	req *proto.DiskDeleteReq,
) (*disk.GcpDiskDeleteOption, error) {
	diskData, err := svc.DataCli.Gcp.RetrieveDisk(kt.Ctx, kt.Header(), req.DiskID)
	if err != nil {
		return nil, err
	}

	return &disk.GcpDiskDeleteOption{Zone: diskData.Zone, DiskName: diskData.Name}, nil
}
