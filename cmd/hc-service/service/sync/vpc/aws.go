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

// Package vpc defines vpc service.
package vpc

import (
	"hcm/cmd/hc-service/logics/sync/vpc"
	"hcm/pkg/adaptor/aws"
	typecore "hcm/pkg/adaptor/types/core"
	"hcm/pkg/api/core"
	dataproto "hcm/pkg/api/data-service"
	"hcm/pkg/api/hc-service/sync"
	"hcm/pkg/criteria/constant"
	"hcm/pkg/criteria/errf"
	"hcm/pkg/dal/dao/tools"
	"hcm/pkg/kit"
	"hcm/pkg/logs"
	"hcm/pkg/rest"
	"hcm/pkg/runtime/filter"
	"hcm/pkg/tools/converter"
)

// SyncAwsVpc sync aws vpc to hcm.
func (v syncVpcSvc) SyncAwsVpc(cts *rest.Contexts) (interface{}, error) {
	req := new(sync.AwsSyncReq)
	if err := cts.DecodeInto(req); err != nil {
		return nil, errf.NewFromErr(errf.DecodeRequestFailed, err)
	}

	if err := req.Validate(); err != nil {
		return nil, errf.NewFromErr(errf.InvalidParameter, err)
	}

	cli, err := v.ad.Aws(cts.Kit, req.AccountID)
	if err != nil {
		return nil, err
	}

	cloudTotalMap := make(map[string]struct{}, 0)
	listOpt := &typecore.AwsListOption{
		Region:   req.Region,
		CloudIDs: nil,
		Page: &typecore.AwsPage{
			MaxResults: converter.ValToPtr(int64(constant.BatchOperationMaxLimit)),
			NextToken:  nil,
		},
	}
	for {
		vpcResult, err := cli.ListVpc(cts.Kit, listOpt)
		if err != nil {
			logs.Errorf("request adaptor list aws vpc failed, err: %v, opt: %v, rid: %s", err, listOpt, cts.Kit.Rid)
			return nil, err
		}

		if len(vpcResult.Details) == 0 {
			break
		}

		cloudIDs := make([]string, 0, len(vpcResult.Details))
		for _, one := range vpcResult.Details {
			cloudTotalMap[one.CloudID] = struct{}{}
			cloudIDs = append(cloudIDs, one.CloudID)
		}

		syncOpt := &vpc.SyncAwsOption{
			AccountID: req.AccountID,
			Region:    req.Region,
			CloudIDs:  cloudIDs,
		}
		_, err = vpc.AwsVpcSync(cts.Kit, v.ad, v.dataCli, syncOpt)
		if err != nil {
			logs.Errorf("request to sync aws vpc failed, err: %v, opt: %v, rid: %s", err, syncOpt, cts.Kit.Rid)
			return nil, err
		}

		if vpcResult.NextToken == nil || len(*vpcResult.NextToken) == 0 {
			break
		}

		listOpt.Page.NextToken = vpcResult.NextToken
	}

	if err = v.delDBNotExistAwsVpc(cts.Kit, cli, req, cloudTotalMap); err != nil {
		logs.Errorf("remove db not exist vpc failed, err: %v, rid: %s", err, cts.Kit.Rid)
		return nil, err
	}

	return nil, nil
}

func (v *syncVpcSvc) delDBNotExistAwsVpc(kt *kit.Kit, aws *aws.Aws, req *sync.AwsSyncReq,
	cloudCvmMap map[string]struct{}) error {

	// 找出云上已经不存在的VpcID
	delCloudVpcIDs, err := v.queryDeleteVpc(kt, req.Region, req.AccountID, cloudCvmMap)
	if err != nil {
		return err
	}

	// 再用这部分VpcID去云上确认是否存在，如果不存在，删除db数据，存在的忽略，等同步修复
	start, end := 0, typecore.AwsQueryLimit
	for {
		if start+end > len(delCloudVpcIDs) {
			end = len(delCloudVpcIDs)
		} else {
			end = int(start) + typecore.AwsQueryLimit
		}
		tmpCloudIDs := delCloudVpcIDs[start:end]

		if len(tmpCloudIDs) == 0 {
			break
		}

		listOpt := &typecore.AwsListOption{
			Region:   req.Region,
			CloudIDs: tmpCloudIDs,
		}
		vpcResult, err := aws.ListVpc(kt, listOpt)
		if err != nil {
			logs.Errorf("list vpc from aws failed, err: %v, opt: %v, rid: %s", err, listOpt, kt.Rid)
			return err
		}

		tmpMap := converter.StringSliceToMap(tmpCloudIDs)
		for _, vpc := range vpcResult.Details {
			delete(tmpMap, vpc.CloudID)
		}

		if len(tmpMap) == 0 {
			start = end
			continue
		}

		if err = v.dataCli.Global.Vpc.BatchDelete(kt.Ctx, kt.Header(), &dataproto.BatchDeleteReq{
			Filter: tools.ContainersExpression("cloud_id", converter.MapKeyToStringSlice(tmpMap)),
		}); err != nil {
			logs.Errorf("batch delete db vpc failed, err: %v, rid: %s", err, kt.Rid)
			return err
		}

		start = end
		if start == len(delCloudVpcIDs) {
			break
		}
	}

	return nil
}

func (v *syncVpcSvc) queryDeleteVpc(kt *kit.Kit, region, accountID string, cloudCvmMap map[string]struct{}) (
	[]string, error) {

	start := uint32(0)
	delCloudVpcIDs := make([]string, 0)
	for {
		listReq := &core.ListReq{
			Fields: []string{"cloud_id"},
			Filter: &filter.Expression{
				Op: filter.And,
				Rules: []filter.RuleFactory{
					&filter.AtomRule{Field: "account_id", Op: filter.Equal.Factory(), Value: accountID},
					&filter.AtomRule{Field: "region", Op: filter.Equal.Factory(), Value: region},
				},
			},
			Page: &core.BasePage{
				Start: start,
				Limit: core.DefaultMaxPageLimit,
			},
		}
		result, err := v.dataCli.Global.Vpc.List(kt.Ctx, kt.Header(), listReq)
		if err != nil {
			logs.Errorf("list vpc from db failed, err: %v, rid: %s", err, kt.Rid)
			return nil, err
		}

		for _, one := range result.Details {
			if _, exist := cloudCvmMap[one.CloudID]; !exist {
				delCloudVpcIDs = append(delCloudVpcIDs, one.CloudID)
			}
		}

		if len(result.Details) < int(core.DefaultMaxPageLimit) {
			break
		}

		start += uint32(core.DefaultMaxPageLimit)
	}
	return delCloudVpcIDs, nil
}
