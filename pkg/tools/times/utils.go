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

package times

import (
	"fmt"
	"time"
)

// GetLastMonth get last month year and month
func GetLastMonth(billYear, billMonth int) (int, int, error) {
	t, err := time.Parse("2006-01-02T15:04:05.000+08:00", fmt.Sprintf("%d-%02d-02T15:04:05.000+08:00", billYear, billMonth))
	if err != nil {
		return 0, 0, err
	}
	lastT := t.AddDate(0, -1, 0)
	return lastT.Year(), int(lastT.Month()), nil
}
