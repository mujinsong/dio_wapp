const auth = require('../../utils/auth')
const adminGoods = require('../../utils/adminGoods')
const operationRequest = require('../../utils/operationRequest')
const schedule = require('../../utils/schedule')

const MAX_GOODS_VALUE = 1000000000
const MAX_PURCHASE_LIMIT_HOURS = 8760

const statusOptions = [
  { label: '上架', value: 'on_sale' },
  { label: '下架', value: 'off_sale' }
]

const approvalFilterOptions = [
  { label: '待审批', value: 'pending' },
  { label: '已批准', value: 'approved' },
  { label: '已驳回', value: 'rejected' },
  { label: '全部', value: 'all' }
]

function emptyGoodsForm() {
  return Object.assign({
    id: 0,
    name: '',
    description: '',
    image_url: '',
    price_points: '',
    stock: '',
    purchase_limit_per_user: '0',
    purchase_limit_hours: '0',
    sort: '',
    status: 'on_sale',
    expected_updated_at: ''
  }, schedule.toForm(null))
}

function emptyGroupForm() {
  return {
    id: 0,
    name: '',
    description: '',
    sort: ''
  }
}

function presentGoods(goods) {
  return goods.map(item => Object.assign({}, item, {
    price_text: `${item.price_points} PTS`,
    stock_text: `库存 ${item.stock}`,
    limit_text: item.purchase_limit_per_user > 0 && item.purchase_limit_hours > 0
      ? `前 ${item.purchase_limit_hours} 小时每人限购 ${item.purchase_limit_per_user}`
      : '不限购',
    status_text: item.status === 'on_sale' ? (schedule.isFuture(item.sale_starts_at) ? '定时上架' : '上架') : '下架',
    sale_time_text: item.sale_starts_at ? `上架 ${schedule.format(item.sale_starts_at)}` : '立即上架',
    initial: (item.name || '商').slice(0, 1)
  }))
}

function presentGroups(groups) {
  return groups.map(group => Object.assign({}, group, {
    initial: (group.name || '团').slice(0, 1),
    description_text: group.description || '暂无说明',
    status_text: group.status === 'normal' ? '启用' : '停用',
    status_class: group.status === 'normal' ? 'group-status' : 'group-status disabled',
    action_text: group.status === 'normal' ? '停用' : '启用'
  }))
}

function presentApprovalRequests(items) {
  const actionText = { create: '新增', update: '修改', delete: '删除' }
  const requestStatusText = { pending: '待审批', approved: '已批准', rejected: '已驳回' }
  return items.map(item => {
    const goodsName = item.name || `商品 #${item.goods_id}`
    let approvalEffectText = `批准后「${goodsName}」将立即生效。`
    if (item.action === 'delete') {
      approvalEffectText = `批准后「${goodsName}」将被删除。`
    } else if (schedule.isFuture(item.sale_starts_at)) {
      approvalEffectText = `批准后「${goodsName}」将在 ${schedule.format(item.sale_starts_at)} 上架。`
    }
    return Object.assign({}, item, {
      action_text: actionText[item.action] || item.action,
      target_status_text: item.target_status === 'on_sale' ? '上架' : '下架',
      goods_name_text: goodsName,
      price_text: item.action === 'delete' ? '删除正式商品' : `${item.price_points} PTS · 库存 ${item.stock}`,
      schedule_text: item.action === 'delete'
        ? '批准后删除'
        : (item.sale_starts_at ? `定时 ${schedule.format(item.sale_starts_at)}` : '立即生效'),
      request_status_text: requestStatusText[item.request_status] || item.request_status,
      request_status_class: `approval-status ${item.request_status}`,
      created_text: schedule.format(item.created_at),
      reviewed_text: item.reviewed_at ? schedule.format(item.reviewed_at) : '',
      approval_effect_text: approvalEffectText
    })
  })
}

function approvalView(requests, filter, totals) {
  const counts = Object.assign({ pending: 0, approved: 0, rejected: 0, all: 0 }, totals || {})
  const emptyText = {
    pending: '暂无待审批申请',
    approved: '暂无已批准记录',
    rejected: '暂无已驳回记录',
    all: '暂无商品审批记录'
  }
  return {
    approvalFilter: filter,
    approvalTabs: approvalFilterOptions.map(item => ({
      label: item.label,
      value: item.value,
      count: counts[item.value],
      tab_class: item.value === filter ? 'approval-tab active' : 'approval-tab'
    })),
    approvalCounts: counts,
    allApprovalRequests: requests,
    approvalRequests: requests,
    hasApprovalRequests: requests.length > 0,
    pendingApprovalCount: counts.pending,
    approvalEmptyText: emptyText[filter]
  }
}

Page({
  data: {
    adminAgencies: [],
    agencyIndex: 0,
    selectedAgencyLabel: '',
    hasAdminAgencies: false,
    groups: [],
    hasGroups: false,
    productGroups: [],
    groupIndex: 0,
    selectedGroupLabel: '',
    hasProductGroups: false,
    groupForm: emptyGroupForm(),
    groupSubmitTitle: '新增团体',
    groupSaving: false,
    goods: [],
    hasGoods: false,
    allApprovalRequests: [],
    approvalRequests: [],
    hasApprovalRequests: false,
    approvalFilter: 'pending',
    approvalTabs: approvalView([], 'pending').approvalTabs,
    approvalCounts: { pending: 0, approved: 0, rejected: 0, all: 0 },
    pendingApprovalCount: 0,
    approvalEmptyText: '暂无待审批申请',
    reviewingRequestId: 0,
    approvalCursor: 0,
    approvalHasMore: false,
    approvalLoadingMore: false,
    statusOptions,
    statusIndex: 0,
    selectedStatusLabel: statusOptions[0].label,
    form: emptyGoodsForm(),
    submitTitle: '新增商品',
    loading: false,
    saving: false,
    pendingAdminGoodsRequest: null,
    error: ''
  },

  onShow() {
    this.loadAdmin()
  },

  loadAdmin() {
    this.setData({ loading: true, error: '' })
    auth.me()
      .then(user => {
        const adminAgencies = Array.isArray(user.admin_agencies) ? user.admin_agencies : []
        this.setAgencyState(adminAgencies, 0)
        if (adminAgencies.length > 0) {
          return this.loadAgencyData(adminAgencies[0].agency_id)
        }
        return null
      })
      .catch(err => this.showError(err, '获取管理员信息失败'))
      .finally(() => {
        this.setData({ loading: false })
      })
  },

  setAgencyState(adminAgencies, agencyIndex) {
    const agency = adminAgencies[agencyIndex]
    this.setData({
      adminAgencies,
      agencyIndex,
      hasAdminAgencies: adminAgencies.length > 0,
      selectedAgencyLabel: agency ? agency.agency_name : '',
      groups: [],
      productGroups: [],
      goods: [],
      allApprovalRequests: [],
      approvalRequests: [],
      hasGroups: false,
      hasProductGroups: false,
      hasGoods: false,
      hasApprovalRequests: false,
      approvalFilter: 'pending',
      approvalTabs: approvalView([], 'pending').approvalTabs,
      approvalCounts: { pending: 0, approved: 0, rejected: 0, all: 0 },
      pendingApprovalCount: 0,
      approvalEmptyText: '暂无待审批申请',
      selectedGroupLabel: '',
      groupIndex: 0,
      groupForm: emptyGroupForm(),
      groupSubmitTitle: '新增团体',
      form: emptyGoodsForm(),
      submitTitle: '新增商品',
      approvalCursor: 0,
      approvalHasMore: false,
      approvalLoadingMore: false
    })
  },

  loadAgencyData(agencyId) {
    this.setData({ loading: true, error: '' })
    return Promise.all([
      adminGoods.listGroups(agencyId),
      adminGoods.listGoods(agencyId),
      adminGoods.listGoodsRequests(this.data.approvalFilter, 0, 20)
    ])
      .then(([groups, goods, approvalPage]) => {
        this.setGroups(groups)
        const presentedApprovals = presentApprovalRequests(approvalPage.items || [])
        this.setData(Object.assign({
          goods: presentGoods(goods),
          hasGoods: goods.length > 0,
          approvalCursor: approvalPage.next_cursor || 0,
          approvalHasMore: !!approvalPage.has_more,
          approvalLoadingMore: false
        }, approvalView(presentedApprovals, this.data.approvalFilter, approvalPage.counts)))
      })
      .catch(err => this.showError(err, '管理数据加载失败'))
      .finally(() => {
        this.setData({ loading: false })
      })
  },

  setGroups(groups, preferredGroupId) {
    const presented = presentGroups(groups)
    const productGroups = presented.filter(group => group.status === 'normal')
    let groupIndex = preferredGroupId
      ? productGroups.findIndex(group => group.id === preferredGroupId)
      : 0
    if (groupIndex < 0) {
      groupIndex = 0
    }
    const selected = productGroups[groupIndex]
    this.setData({
      groups: presented,
      hasGroups: presented.length > 0,
      productGroups,
      hasProductGroups: productGroups.length > 0,
      groupIndex,
      selectedGroupLabel: selected ? selected.name : ''
    })
  },

  handleApprovalFilter(event) {
    const filter = event.currentTarget.dataset.status
    if (!approvalFilterOptions.some(item => item.value === filter)) {
      return
    }
    if (filter === this.data.approvalFilter) {
      return
    }
    this.loadApprovalRequests(filter, true)
  },

  loadApprovalRequests(filter, reset) {
    if (this.data.approvalLoadingMore) {
      return Promise.resolve()
    }
    const cursor = reset ? 0 : this.data.approvalCursor
    this.setData({ approvalLoadingMore: true, error: '' })
    return adminGoods.listGoodsRequests(filter, cursor, 20)
      .then(page => {
        const incoming = presentApprovalRequests(page.items || [])
        const requests = reset ? incoming : this.data.allApprovalRequests.concat(incoming)
        this.setData(Object.assign({
          approvalCursor: page.next_cursor || 0,
          approvalHasMore: !!page.has_more
        }, approvalView(requests, filter, page.counts || this.data.approvalCounts)))
      })
      .catch(err => this.showError(err, '商品审批记录加载失败'))
      .finally(() => this.setData({ approvalLoadingMore: false }))
  },

  loadMoreApprovalRequests() {
    if (!this.data.approvalHasMore || this.data.approvalLoadingMore) {
      return
    }
    this.loadApprovalRequests(this.data.approvalFilter, false)
  },

  handleGroupChange(event) {
    const groupIndex = Number(event.detail.value || 0)
    const group = this.data.productGroups[groupIndex]
    this.setData({
      groupIndex,
      selectedGroupLabel: group ? group.name : ''
    })
  },

  handleGroupInput(event) {
    const field = event.currentTarget.dataset.groupField
    if (!field) {
      return
    }
    this.setData({
      groupForm: Object.assign({}, this.data.groupForm, { [field]: event.detail.value || '' })
    })
  },

  submitGroup() {
    if (this.data.groupSaving) {
      return
    }
    const agency = this.data.adminAgencies[this.data.agencyIndex]
    const form = this.data.groupForm
    if (!agency || !form.name.trim()) {
      wx.showToast({ title: '请输入团体名称', icon: 'none' })
      return
    }
    const payload = {
      agency_id: agency.agency_id,
      name: form.name.trim(),
      description: form.description.trim(),
      sort: Number(form.sort || 0),
      status: form.id ? (this.data.groups.find(group => group.id === form.id) || {}).status : 'normal'
    }
    if (!Number.isInteger(payload.sort)) {
      wx.showToast({ title: '请输入正确排序', icon: 'none' })
      return
    }

    this.setData({ groupSaving: true, error: '' })
    const action = form.id
      ? adminGoods.updateGroup(form.id, payload)
      : adminGoods.createGroup(payload)
    action
      .then(() => {
        wx.showToast({ title: form.id ? '团体已保存' : '团体已新增', icon: 'success' })
        this.resetGroupForm()
        return this.loadAgencyData(agency.agency_id)
      })
      .catch(err => this.showError(err, '团体保存失败'))
      .finally(() => {
        this.setData({ groupSaving: false })
      })
  },

  editGroup(event) {
    const id = Number(event.currentTarget.dataset.id)
    const group = this.data.groups.find(item => item.id === id)
    if (!group) {
      return
    }
    this.setData({
      groupForm: {
        id: group.id,
        name: group.name,
        description: group.description || '',
        sort: String(group.sort || 0)
      },
      groupSubmitTitle: '保存团体'
    })
  },

  toggleGroup(event) {
    const id = Number(event.currentTarget.dataset.id)
    const group = this.data.groups.find(item => item.id === id)
    const agency = this.data.adminAgencies[this.data.agencyIndex]
    if (!group || !agency) {
      return
    }
    const disabling = group.status === 'normal'
    wx.showModal({
      title: disabling ? '停用团体' : '启用团体',
      content: disabling
        ? `停用「${group.name}」后，该团体商品将从商店隐藏。`
        : `确定重新启用「${group.name}」吗？`,
      confirmText: disabling ? '停用' : '启用',
      confirmColor: disabling ? '#c04736' : '#207f79',
      success: res => {
        if (!res.confirm) {
          return
        }
        const action = disabling
          ? adminGoods.deleteGroup(group.id)
          : adminGoods.updateGroup(group.id, {
            agency_id: agency.agency_id,
            name: group.name,
            description: group.description || '',
            sort: group.sort || 0,
            status: 'normal'
          })
        action
          .then(() => {
            wx.showToast({ title: disabling ? '团体已停用' : '团体已启用', icon: 'success' })
            this.resetGroupForm()
            return this.loadAgencyData(agency.agency_id)
          })
          .catch(err => this.showError(err, disabling ? '停用失败' : '启用失败'))
      }
    })
  },

  resetGroupForm() {
    this.setData({
      groupForm: emptyGroupForm(),
      groupSubmitTitle: '新增团体'
    })
  },

  handleStatusChange(event) {
    const statusIndex = Number(event.detail.value || 0)
    const option = this.data.statusOptions[statusIndex] || this.data.statusOptions[0]
    this.setData({
      statusIndex,
      selectedStatusLabel: option.label,
      form: Object.assign({}, this.data.form, {
        status: option.value,
        schedule_enabled: option.value === 'on_sale' ? this.data.form.schedule_enabled : false
      })
    })
  },

  handleScheduleToggle(event) {
    this.setData({ form: Object.assign({}, this.data.form, { schedule_enabled: Boolean(event.detail.value) }) })
  },

  handleSaleDate(event) {
    this.setData({ form: Object.assign({}, this.data.form, { sale_start_date: event.detail.value }) })
  },

  handleSaleTime(event) {
    this.setData({ form: Object.assign({}, this.data.form, { sale_start_time: event.detail.value }) })
  },

  handleInput(event) {
    const field = event.currentTarget.dataset.field
    if (!field) {
      return
    }
    this.setData({
      form: Object.assign({}, this.data.form, { [field]: event.detail.value || '' })
    })
  },

  submit() {
    if (this.data.saving) {
      return
    }
    const agency = this.data.adminAgencies[this.data.agencyIndex]
    const group = this.data.productGroups[this.data.groupIndex]
    const form = this.data.form
    if (!agency || !group) {
      wx.showToast({ title: '请先新增并启用团体', icon: 'none' })
      return
    }
    if (!form.name.trim()) {
      wx.showToast({ title: '请输入商品名称', icon: 'none' })
      return
    }

    const saleStartsAt = schedule.toISO(form)
    if (form.schedule_enabled && !saleStartsAt) {
      wx.showToast({ title: '请选择正确上架时间', icon: 'none' })
      return
    }
    const payload = {
      agency_id: agency.agency_id,
      group_id: group.id,
      name: form.name.trim(),
      description: form.description.trim(),
      image_url: form.image_url.trim(),
      price_points: Number(form.price_points),
      stock: Number(form.stock),
      purchase_limit_per_user: Number(form.purchase_limit_per_user || 0),
      purchase_limit_hours: Number(form.purchase_limit_hours || 0),
      sale_starts_at: saleStartsAt,
      status: form.status,
      sort: Number(form.sort || 0)
    }
    if (form.id) {
      payload.expected_updated_at = form.expected_updated_at
    }
    if (!Number.isInteger(payload.price_points) || payload.price_points <= 0 || payload.price_points > MAX_GOODS_VALUE) {
      wx.showToast({ title: '积分价格需为 1 至 10 亿', icon: 'none' })
      return
    }
    if (!Number.isInteger(payload.stock) || payload.stock < 0 || payload.stock > MAX_GOODS_VALUE) {
      wx.showToast({ title: '库存需为 0 至 10 亿', icon: 'none' })
      return
    }
    if (!Number.isInteger(payload.purchase_limit_per_user) || payload.purchase_limit_per_user < 0 ||
        payload.purchase_limit_per_user > MAX_GOODS_VALUE) {
      wx.showToast({ title: '限购数量需为 0 至 10 亿', icon: 'none' })
      return
    }
    if (!Number.isInteger(payload.purchase_limit_hours) || payload.purchase_limit_hours < 0 ||
        payload.purchase_limit_hours > MAX_PURCHASE_LIMIT_HOURS) {
      wx.showToast({ title: '限购时长需为 0 至 8760 小时', icon: 'none' })
      return
    }
    if ((payload.purchase_limit_per_user === 0) !== (payload.purchase_limit_hours === 0)) {
      wx.showToast({ title: '限购数量和时长需同时填写', icon: 'none' })
      return
    }

    let operation = null
    if (!form.id) {
      operation = operationRequest.resolve('admin_goods', this.data.pendingAdminGoodsRequest, payload, true)
      payload.request_id = operation.id
    }
    this.setData({
      saving: true,
      error: '',
      pendingAdminGoodsRequest: operation || this.data.pendingAdminGoodsRequest
    })
    const action = form.id ? adminGoods.updateGoods(form.id, payload) : adminGoods.createGoods(payload)
    action
      .then(() => {
        if (operation) {
          operationRequest.clear('admin_goods', operation.id, true)
        }
        wx.showToast({ title: form.id ? '已保存' : '已新增', icon: 'success' })
        this.setData({ pendingAdminGoodsRequest: null })
        this.resetForm()
        return this.loadAgencyData(agency.agency_id)
      })
      .catch(err => this.showError(err, '保存失败'))
      .finally(() => {
        this.setData({ saving: false })
      })
  },

  editGoods(event) {
    const id = Number(event.currentTarget.dataset.id)
    const item = this.data.goods.find(goods => goods.id === id)
    if (!item) {
      return
    }
    let groupIndex = this.data.productGroups.findIndex(group => group.id === item.group_id)
    if (groupIndex < 0) {
      groupIndex = 0
      wx.showToast({ title: '原团体已停用，请重新选择', icon: 'none' })
    }
    const group = this.data.productGroups[groupIndex]
    const statusIndex = item.status === 'off_sale' ? 1 : 0
    const scheduleForm = schedule.toForm(item.sale_starts_at)
    this.setData({
      groupIndex,
      selectedGroupLabel: group ? group.name : '',
      statusIndex,
      selectedStatusLabel: this.data.statusOptions[statusIndex].label,
      submitTitle: '保存修改',
      form: Object.assign({
        id: item.id,
        name: item.name,
        description: item.description || '',
        image_url: item.image_url || '',
        price_points: String(item.price_points),
        stock: String(item.stock),
        purchase_limit_per_user: String(item.purchase_limit_per_user || 0),
        purchase_limit_hours: String(item.purchase_limit_hours || 0),
        sort: String(item.sort || 0),
        status: item.status,
        expected_updated_at: item.updated_at
      }, scheduleForm)
    })
  },

  deleteGoods(event) {
    const id = Number(event.currentTarget.dataset.id)
    const item = this.data.goods.find(goods => goods.id === id)
    const agency = this.data.adminAgencies[this.data.agencyIndex]
    if (!item || !agency) {
      return
    }

    wx.showModal({
      title: '删除商品',
      content: `确定删除「${item.name}」吗？`,
      confirmText: '删除',
      confirmColor: '#c04736',
      success: res => {
        if (!res.confirm) {
          return
        }
        adminGoods.deleteGoods(id)
          .then(() => {
            wx.showToast({ title: '已删除', icon: 'success' })
            return this.loadAgencyData(agency.agency_id)
          })
          .catch(err => this.showError(err, '删除失败'))
      }
    })
  },

  approveGoodsRequest(event) {
    if (this.data.reviewingRequestId) {
      return
    }
    const id = Number(event.currentTarget.dataset.id)
    const item = this.data.approvalRequests.find(request => request.id === id)
    const agency = this.data.adminAgencies[this.data.agencyIndex]
    if (!item || !agency) {
      return
    }
    wx.showModal({
      title: `批准${item.action_text}申请`,
      content: item.approval_effect_text,
      confirmText: '批准',
      confirmColor: '#207f79',
      success: res => {
        if (!res.confirm) {
          return
        }
        this.reviewGoodsRequest(id, true, '', agency.agency_id)
      }
    })
  },

  rejectGoodsRequest(event) {
    if (this.data.reviewingRequestId) {
      return
    }
    const id = Number(event.currentTarget.dataset.id)
    const item = this.data.approvalRequests.find(request => request.id === id)
    const agency = this.data.adminAgencies[this.data.agencyIndex]
    if (!item || !agency) {
      return
    }
    wx.showModal({
      title: `驳回${item.action_text}申请`,
      editable: true,
      placeholderText: '填写至少 2 个字的驳回原因',
      confirmText: '驳回',
      confirmColor: '#c04736',
      success: res => {
        if (!res.confirm) {
          return
        }
        const remark = (res.content || '').trim()
        if (remark.length < 2) {
          wx.showToast({ title: '请填写至少 2 个字的驳回原因', icon: 'none' })
          return
        }
        this.reviewGoodsRequest(id, false, remark, agency.agency_id)
      }
    })
  },

  reviewGoodsRequest(id, approve, remark, agencyId) {
    this.setData({ reviewingRequestId: id, error: '' })
    const action = approve
      ? adminGoods.approveGoodsRequest(id, remark)
      : adminGoods.rejectGoodsRequest(id, remark)
    action
      .then(() => {
        wx.showToast({ title: approve ? '已批准' : '已驳回', icon: 'success' })
        return this.loadAgencyData(agencyId)
      })
      .catch(err => this.showError(err, approve ? '批准失败' : '驳回失败'))
      .finally(() => this.setData({ reviewingRequestId: 0 }))
  },

  resetForm() {
    this.setData({
      form: emptyGoodsForm(),
      statusIndex: 0,
      selectedStatusLabel: this.data.statusOptions[0].label,
      submitTitle: '新增商品'
    })
  },

  showError(err, fallback) {
    const messages = {
      goods_snapshot_changed: '商品在编辑期间已发生变化，请刷新后重新修改',
      missing_goods_version: '商品版本信息缺失，请刷新后重新修改'
    }
    const message = messages[err.code] || err.message || fallback
    this.setData({ error: message })
    wx.showToast({ title: message, icon: 'none' })
    if (err.code === 'goods_snapshot_changed' || err.code === 'missing_goods_version') {
      const agency = this.data.adminAgencies[this.data.agencyIndex]
      this.resetForm()
      if (agency) {
        this.loadAgencyData(agency.agency_id)
      }
    }
  }
})
