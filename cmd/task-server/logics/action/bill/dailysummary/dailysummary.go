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

package dailysummary

import (
	"fmt"

	actcli "hcm/cmd/task-server/logics/action/cli"
	"hcm/pkg/api/core"
	"hcm/pkg/api/data-service/bill"
	"hcm/pkg/async/action"
	"hcm/pkg/async/action/run"
	"hcm/pkg/criteria/constant"
	"hcm/pkg/criteria/enumor"
	"hcm/pkg/criteria/errf"
	"hcm/pkg/dal/dao/tools"
	"hcm/pkg/logs"
	"hcm/pkg/runtime/filter"

	"github.com/shopspring/decimal"
)

// DailySummaryOption option for daily summary
type DailySummaryOption struct {
	RootAccountID string        `json:"root_account_id" validate:"required"`
	MainAccountID string        `json:"main_account_id" validate:"required"`
	ProductID     int64         `json:"product_id" validate:"required"`
	BkBizID       int64         `json:"bk_biz_id" validate:"required"`
	BillYear      int           `json:"bill_year" validate:"required"`
	BillMonth     int           `json:"bill_month" validate:"required"`
	BillDay       int           `json:"bill_day" validate:"required"`
	VersionID     int           `json:"version_id" validate:"required"`
	Vendor        enumor.Vendor `json:"vendor" validate:"required"`
}

var _ action.Action = new(DailySummaryAction)
var _ action.ParameterAction = new(DailySummaryAction)

// DailySummaryAction define daily summary action
type DailySummaryAction struct{}

// ParameterNew return request params.
func (act DailySummaryAction) ParameterNew() interface{} {
	return new(DailySummaryOption)
}

// Name return action name
func (act DailySummaryAction) Name() enumor.ActionName {
	return enumor.ActionDailyAccountSummary
}

func getFilter(opt *DailySummaryOption, billDay int) *filter.Expression {
	var expressions []*filter.AtomRule
	expressions = append(expressions, []*filter.AtomRule{
		tools.RuleEqual("root_account_id", opt.RootAccountID),
		tools.RuleEqual("main_account_id", opt.MainAccountID),
		tools.RuleEqual("product_id", opt.ProductID),
		tools.RuleEqual("bk_biz_id", opt.BkBizID),
		tools.RuleEqual("vendor", opt.Vendor),
		tools.RuleEqual("version_id", opt.VersionID),
		tools.RuleEqual("bill_year", opt.BillYear),
		tools.RuleEqual("bill_month", opt.BillMonth),
	}...)
	if billDay != 0 {
		expressions = append(expressions, tools.RuleEqual("bill_day", billDay))
	}
	return tools.ExpressionAnd(expressions...)
}

// Run run pull daily bill
func (act DailySummaryAction) Run(kt run.ExecuteKit, params interface{}) (interface{}, error) {
	opt, ok := params.(*DailySummaryOption)
	if !ok {
		return nil, errf.New(errf.InvalidParameter, "params type mismatch")
	}

	pullTaskList, err := actcli.GetDataService().Global.Bill.ListBillDailyPullTask(
		kt.Kit(), &bill.BillDailyPullTaskListReq{
			Filter: getFilter(opt, opt.BillDay),
			Page: &core.BasePage{
				Start: 0,
				Limit: 1,
			},
		})
	if err != nil {
		return nil, fmt.Errorf("list pull task by opt %v failed, err %s", opt, err.Error())
	}
	if len(pullTaskList.Details) != 1 {
		return nil, fmt.Errorf("get pull task invalid length, resp %v", pullTaskList.Details)
	}
	task := pullTaskList.Details[0]
	if task.State == constant.MainAccountRawBillPullStateSplitted {
		if err := act.doDailySummary(kt, opt, task.BillDay); err != nil {
			logs.Infof("do daily summary for task %v failed, err %s, rid %s", task, err.Error(), kt.Kit().Rid)
			return nil, err
		}
		if err := act.changeTaskToAccounted(kt, task); err != nil {
			logs.Infof("change pull task %v to accounted state failed, err %s, rid %s",
				task, err.Error(), kt.Kit().Rid)
			return nil, err
		}
	}
	return nil, nil
}

func (act DailySummaryAction) doDailySummary(kt run.ExecuteKit, opt *DailySummaryOption, billDay int) error {
	result, err := actcli.GetDataService().Global.Bill.ListBillItem(kt.Kit(), &bill.BillItemListReq{
		Filter: getFilter(opt, billDay),
		Page: &core.BasePage{
			Count: true,
		},
	})
	if err != nil {
		return fmt.Errorf("count bill item for %v day %d failed, err %s", opt, billDay, err.Error())
	}

	currency := enumor.CurrencyUSD
	cost := decimal.NewFromFloat(0)
	count := result.Count

	limit := uint64(500)
	for start := uint64(0); start < result.Count; start = start + limit {
		result, err := actcli.GetDataService().Global.Bill.ListBillItem(kt.Kit(), &bill.BillItemListReq{
			Filter: getFilter(opt, billDay),
			Page: &core.BasePage{
				Start: uint32(start),
				Limit: uint(limit),
			},
		})
		if err != nil {
			return fmt.Errorf("get %d-%d bill item for %v day %d failed, err %s",
				start, limit, opt, billDay, err.Error())
		}
		for _, item := range result.Details {
			if len(item.Currency) != 0 && len(currency) == 0 {
				currency = item.Currency
			}
			cost = cost.Add(item.Cost)
		}
	}
	return act.syncDailySummary(kt, opt, billDay, currency, cost, count)
}

func (act DailySummaryAction) changeTaskToAccounted(
	kt run.ExecuteKit, billTask *bill.BillDailyPullTaskResult) error {

	return actcli.GetDataService().Global.Bill.UpdateBillDailyPullTask(
		kt.Kit(), &bill.BillDailyPullTaskUpdateReq{
			ID:    billTask.ID,
			State: constant.MainAccountRawBillPullStateAccounted,
		})
}

func (act DailySummaryAction) syncDailySummary(kt run.ExecuteKit, opt *DailySummaryOption,
	billDay int, currency enumor.CurrencyCode, cost decimal.Decimal, count uint64) error {

	result, err := actcli.GetDataService().Global.Bill.ListBillSummaryDaily(kt.Kit(), &bill.BillSummaryDailyListReq{
		Filter: getFilter(opt, billDay),
		Page: &core.BasePage{
			Start: 0,
			Limit: 1,
		},
	})
	if err != nil {
		return fmt.Errorf("get daily summary for %v day %d failed, err %s", opt, billDay, err.Error())
	}
	if len(result.Details) == 0 {
		if _, err := actcli.GetDataService().Global.Bill.CreateBillSummaryDaily(kt.Kit(),
			&bill.BillSummaryDailyCreateReq{
				RootAccountID: opt.RootAccountID,
				MainAccountID: opt.MainAccountID,
				ProductID:     opt.ProductID,
				BkBizID:       opt.BkBizID,
				Vendor:        opt.Vendor,
				BillYear:      opt.BillYear,
				BillMonth:     opt.BillMonth,
				BillDay:       billDay,
				VersionID:     opt.VersionID,
				Currency:      currency,
				Cost:          cost,
				Count:         int64(count),
			}); err != nil {
			return fmt.Errorf("create daily summary for %v day %d failed, err %s", opt, billDay, err.Error())
		}
		logs.Infof("create daily summary for %v day %d successfully cost %s count %d",
			opt, billDay, cost.String(), count)
		return nil
	}
	if len(result.Details) != 1 {
		return fmt.Errorf("get daily summary for %v day %d failed, invalid resp %v", opt, billDay, result.Details)
	}
	summary := result.Details[0]
	if err := actcli.GetDataService().Global.Bill.UpdateBillSummaryDaily(kt.Kit(), &bill.BillSummaryDailyUpdateReq{
		ID:       summary.ID,
		Currency: currency,
		Cost:     &cost,
		Count:    int64(count),
	}); err != nil {
		return fmt.Errorf("update daily summary for %v day %d failed, err %s", opt, billDay, err.Error())
	}
	logs.Infof("update daily summary for %v day %d successfully cost %s count %d, rid: %s",
		opt, billDay, cost.String(), count, kt.Kit().Rid)
	return nil
}
