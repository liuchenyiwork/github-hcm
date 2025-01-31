<script lang="ts" setup>
import { computed, ref, watchEffect, defineExpose, PropType } from 'vue';
import { useRouter, useRoute } from 'vue-router';
import { useAccountStore } from '@/store';
import { isEmpty } from '@/common/util';
import { useI18n } from 'vue-i18n';

const props = defineProps({
  modelValue: [Number, String, Array] as PropType<number | string | Array<number | string>>,
  authed: Boolean as PropType<boolean>,
  autoSelect: Boolean as PropType<boolean>,
  isAudit: Boolean as PropType<boolean>,
  multiple: Boolean as PropType<boolean>,
  clearable: Boolean as PropType<boolean>,
  isShowAll: Boolean as PropType<boolean>,
  notAutoSelectAll: Boolean as PropType<boolean>,
  saveBizs: Boolean as PropType<boolean>,
  bizsKey: String as PropType<string>,
});
const emit = defineEmits(['update:modelValue']);

const router = useRouter();
const route = useRoute();
const { t } = useI18n();
const accountStore = useAccountStore();
const businessList = ref([]);
const defaultBusiness = ref();
const loading = ref(null);

watchEffect(async () => {
  loading.value = true;
  let req = props.authed ? accountStore.getBizListWithAuth : accountStore.getBizList;
  if (props.isAudit) {
    req = accountStore.getBizAuditListWithAuth;
  }

  const res = await req();
  loading.value = false;
  businessList.value = res?.data;

  // 支持全选
  if (props.isShowAll) {
    businessList.value.unshift({
      name: t('全部'),
      id: 'all',
    });
  }

  let id = null;
  // 自动选中的默认值
  if (props.autoSelect && !isEmpty(businessList.value)) {
    // 支持全选, 若开启 notAutoSelectAll 则选中第一个业务; 若未开启, 则选中"全选"
    id =
      props.isShowAll && props.notAutoSelectAll && businessList.value[1]
        ? businessList.value[1].id
        : businessList.value[0]?.id;
  }
  // 开启 saveBizs, 则自动选中上一次选中的业务
  if (props.saveBizs) {
    // 如果存在已保存的业务id, 则使用; 否则, 取默认值
    id = route.query?.[props.bizsKey] ? JSON.parse(atob(route.query?.[props.bizsKey] as string)) : id;
  }
  // 支持多选
  if (props.multiple) {
    // 如果存在已保存的业务id, 则直接使用(解码后是一个数组); 否则, 需要套一个数组(默认值为 string 类型)
    id = route.query?.[props.bizsKey] ? id : [id];
  }

  // 设置选中的值
  defaultBusiness.value = id;
  selectedValue.value = id;
  // 记录业务id 到 url 上
  handleChange(id);
});

const selectedValue = computed({
  get() {
    if (!isEmpty(props.modelValue)) {
      return props.modelValue;
    }
    if (props.isShowAll) {
      if (props.multiple && Array.isArray(props.modelValue) && props.modelValue.length === 0) {
        return ['all'];
      }
      if (!props.multiple && props.modelValue === '') {
        return 'all';
      }
    }
    return props.multiple ? [] : null;
  },
  set(val) {
    let selectedValue = val;
    if (props.isShowAll) {
      if (props.multiple && Array.isArray(selectedValue)) {
        if (selectedValue[selectedValue.length - 1] === 'all') {
          selectedValue = [];
        } else if (selectedValue.includes('all')) {
          const index = selectedValue.findIndex((val) => val === 'all');
          selectedValue.splice(index, 1);
        }
      }
      if (!props.multiple && selectedValue === 'all') {
        selectedValue = '';
      }
    }
    emit('update:modelValue', selectedValue);
  },
});

const handleChange = (val: string | string[]) => {
  if (props.saveBizs) {
    const hasAll = Array.isArray(val) && val.includes('all');
    // 考虑到数组可能有多个元素, 使用 base64 配合 JSON 进行转码
    const customBizs = btoa(JSON.stringify(val));
    router.push({
      query: {
        ...route.query,
        // 全选时, 不用存业务id
        [props.bizsKey]: hasAll || !val ? undefined : customBizs,
      },
    });
  }
};

defineExpose({
  businessList,
  defaultBusiness,
});
</script>

<template>
  <bk-select
    v-model="selectedValue"
    :multiple="multiple"
    filterable
    :loading="loading"
    :clearable="clearable"
    @change="handleChange"
  >
    <bk-option v-for="item in businessList" :key="item.id" :value="item.id" :label="item.name" />
  </bk-select>
</template>
