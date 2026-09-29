const auth = require('../../utils/auth')
const staffGoods = require('../../utils/staffGoods')
const operationRequest = require('../../utils/operationRequest')
const schedule = require('../../utils/schedule')

const MAX_GOODS_VALUE = 1000000000
const MAX_PURCHASE_LIMIT_HOURS = 8760

const statusOptions = [
  { label: '上架', value: 'on_sale' },
  { label: '下架', value: 'off_sale' }
]

function emptyForm() {
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
    status: 'on_sale'
  }, schedule.toForm(null))
}

function presentGoods(items) {
  return items.map(item => Object.assign({}, item, {
    price_text: `${item.price_points} PTS`,
    stock_text: `库存 ${item.stock}`,
    status_text: item.status === 'on_sale' ? (schedule.isFuture(item.sale_starts_at) ? '定时上架' : '上架') : '下架',
    sale_time_text: item.sale_starts_at ? `上架 ${schedule.format(item.sale_starts_at)}` : '立即上架',
    limit_text: item.purchase_limit_per_user > 0 && item.purchase_limit_hours > 0
      ? `前 ${item.purchase_limit_hours} 小时限购 ${item.purchase_limit_per_user}`
      : '不限购',
    initial: (item.name || '商').slice(0, 1)
  }))
}

function presentRequests(items, isTeamLeader, currentUserId) {
  const actionText = { create: '新增', update: '修改', delete: '删除' }
  const statusText = { pending: '待审批', approved: '已批准', rejected: '已驳回' }
  return items.map(item => Object.assign({}, item, {
    action_text: actionText[item.action] || item.action,
    request_status_text: statusText[item.request_status] || item.request_status,
    request_status_class: `request-status ${item.request_status}`,
    name_text: item.name || `商品 #${item.goods_id}`,
    review_text: item.review_remark || (item.request_status === 'pending' ? (isTeamLeader ? '等待团队负责人或管理员处理' : '等待负责人或管理员处理') : '未填写审批说明'),
    submitter_text: `提交人 #${item.submitted_by}`,
    can_review: isTeamLeader && item.request_status === 'pending' && item.submitted_by !== currentUserId,
    schedule_text: item.action === 'delete'
      ? '批准后删除'
      : (item.sale_starts_at ? `定时 ${schedule.format(item.sale_starts_at)}` : '立即生效')
  }))
}

function groupMode(group) {
  const isTeamLeader = Boolean(group && group.member_role === 'team_leader')
  return {
    isTeamLeader,
    workspaceTitle: isTeamLeader ? '团队商品' : '商品申请',
    workspaceSubtitle: isTeamLeader ? '直接管理商品并审批团队申请' : '商品变更经负责人或管理员批准后生效',
    submitSectionTitle: isTeamLeader ? '管理团队商品' : '提交商品变更',
    submitSectionNote: isTeamLeader ? '保存后直接生效，并保留操作记录' : '编辑现有商品或填写新商品信息',
    officialGoodsNote: isTeamLeader ? '这里展示当前正式生效的商品' : '待审批申请不会直接改变这里的数据',
    requestSectionTitle: isTeamLeader ? '团队申请' : '申请记录',
    requestSectionNote: isTeamLeader ? '可审批当前团队工作人员的申请' : '仅显示你在当前团体提交的申请',
    submitTitle: isTeamLeader ? '直接新增商品' : '提交新增申请'
  }
}

Page({
  data: {
    staffGroups: [],
    groupIndex: 0,
    selectedGroupLabel: '',
    hasStaffGroups: false,
    currentUserId: 0,
    isTeamLeader: false,
    workspaceTitle: '商品申请',
    workspaceSubtitle: '商品变更经负责人或管理员批准后生效',
    submitSectionTitle: '提交商品变更',
    submitSectionNote: '编辑现有商品或填写新商品信息',
    officialGoodsNote: '待审批申请不会直接改变这里的数据',
    requestSectionTitle: '申请记录',
    requestSectionNote: '仅显示你在当前团体提交的申请',
    goods: [],
    hasGoods: false,
    requests: [],
    hasRequests: false,
    requestCursor: 0,
    requestHasMore: false,
    requestLoadingMore: false,
    statusOptions,
    statusIndex: 0,
    selectedStatusLabel: statusOptions[0].label,
    form: emptyForm(),
    submitTitle: '提交新增申请',
    loading: false,
    saving: false,
    pendingGoodsRequest: null,
    error: ''
  },

  onShow() {
    this.loadIdentity()
  },

  onReachBottom() {
    this.loadMoreRequests()
  },

  loadIdentity() {
    this.setData({ loading: true, error: '' })
    auth.me()
      .then(user => {
        const staffGroups = Array.isArray(user.staff_groups)
          ? user.staff_groups.map(group => Object.assign({ member_role: 'staff' }, group))
          : []
        const selected = staffGroups[0]
        this.setData(Object.assign({
          staffGroups,
          groupIndex: 0,
          selectedGroupLabel: selected ? selected.group_name : '',
          hasStaffGroups: staffGroups.length > 0,
          currentUserId: user.id || 0
        }, groupMode(selected)))
        return selected ? this.loadGroupData(selected.group_id, selected.member_role === 'team_leader', user.id || 0) : null
      })
      .catch(err => this.showError(err, '获取工作人员权限失败'))
      .finally(() => this.setData({ loading: false }))
  },

  loadGroupData(groupId, isTeamLeader, currentUserId) {
    const loadVersion = (this.groupLoadVersion || 0) + 1
    this.groupLoadVersion = loadVersion
    this.setData({ loading: true, error: '' })
    const requestsPromise = isTeamLeader
      ? staffGoods.listTeamRequests(groupId, 'all', 0, 20)
      : staffGoods.listRequests(groupId, 0, 20)
    return Promise.all([staffGoods.listGoods(groupId), requestsPromise])
      .then(([goods, requestPage]) => {
        if (this.groupLoadVersion !== loadVersion) {
          return
        }
        const requests = requestPage.items || []
        this.setData({
          goods: presentGoods(goods),
          hasGoods: goods.length > 0,
          requests: presentRequests(requests, isTeamLeader, currentUserId),
          hasRequests: requests.length > 0,
          requestCursor: requestPage.next_cursor || 0,
          requestHasMore: !!requestPage.has_more,
          requestLoadingMore: false
        })
      })
      .catch(err => {
        if (this.groupLoadVersion === loadVersion) {
          this.showError(err, '商品申请数据加载失败')
        }
      })
      .finally(() => {
        if (this.groupLoadVersion === loadVersion) {
          this.setData({ loading: false })
        }
      })
  },

  loadMoreRequests() {
    if (!this.data.requestHasMore || this.data.requestLoadingMore || this.data.loading) {
      return
    }
    const group = this.data.staffGroups[this.data.groupIndex]
    if (!group) {
      return
    }
    this.setData({ requestLoadingMore: true })
    const action = this.data.isTeamLeader
      ? staffGoods.listTeamRequests(group.group_id, 'all', this.data.requestCursor, 20)
      : staffGoods.listRequests(group.group_id, this.data.requestCursor, 20)
    action
      .then(page => {
        const currentGroup = this.data.staffGroups[this.data.groupIndex]
        if (!currentGroup || currentGroup.group_id !== group.group_id) {
          return
        }
        const incoming = presentRequests(page.items || [], this.data.isTeamLeader, this.data.currentUserId)
        this.setData({
          requests: this.data.requests.concat(incoming),
          requestCursor: page.next_cursor || 0,
          requestHasMore: !!page.has_more
        })
      })
      .catch(err => this.showError(err, '加载更多申请失败'))
      .finally(() => this.setData({ requestLoadingMore: false }))
  },

  handleGroupChange(event) {
    const groupIndex = Number(event.detail.value || 0)
    const group = this.data.staffGroups[groupIndex]
    this.setData(Object.assign({
      groupIndex,
      selectedGroupLabel: group ? group.group_name : '',
      form: emptyForm(),
      statusIndex: 0,
      selectedStatusLabel: statusOptions[0].label
    }, groupMode(group)))
    if (group) {
      this.loadGroupData(group.group_id, group.member_role === 'team_leader', this.data.currentUserId)
    }
  },

  handleInput(event) {
    const field = event.currentTarget.dataset.field
    if (!field) {
      return
    }
    this.setData({ form: Object.assign({}, this.data.form, { [field]: event.detail.value || '' }) })
  },

  handleStatusChange(event) {
    const statusIndex = Number(event.detail.value || 0)
    const option = statusOptions[statusIndex] || statusOptions[0]
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

  submit() {
    if (this.data.saving) {
      return
    }
    const group = this.data.staffGroups[this.data.groupIndex]
    const form = this.data.form
    if (!group) {
      wx.showToast({ title: '当前没有可管理团体', icon: 'none' })
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
      action: form.id ? 'update' : 'create',
      group_id: group.group_id,
      goods_id: form.id || 0,
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
    if (!Number.isInteger(payload.price_points) || payload.price_points <= 0 || payload.price_points > MAX_GOODS_VALUE) {
      wx.showToast({ title: '积分价格需为 1 至 10 亿', icon: 'none' })
      return
    }
    if (!Number.isInteger(payload.stock) || payload.stock < 0 || payload.stock > MAX_GOODS_VALUE) {
      wx.showToast({ title: '库存需为 0 至 10 亿', icon: 'none' })
      return
    }
    if (!Number.isInteger(payload.purchase_limit_per_user) || payload.purchase_limit_per_user < 0 ||
        payload.purchase_limit_per_user > MAX_GOODS_VALUE ||
        !Number.isInteger(payload.purchase_limit_hours) || payload.purchase_limit_hours < 0 ||
        payload.purchase_limit_hours > MAX_PURCHASE_LIMIT_HOURS) {
      wx.showToast({ title: '请输入正确限购设置', icon: 'none' })
      return
    }
    if ((payload.purchase_limit_per_user === 0) !== (payload.purchase_limit_hours === 0)) {
      wx.showToast({ title: '限购数量和时长需同时填写', icon: 'none' })
      return
    }
    if (!Number.isInteger(payload.sort)) {
      wx.showToast({ title: '请输入正确排序', icon: 'none' })
      return
    }

    const operation = operationRequest.resolve('staff_goods', this.data.pendingGoodsRequest, payload, true)
    this.setData({ saving: true, error: '', pendingGoodsRequest: operation })
    staffGoods.submit(Object.assign({}, payload, { request_id: operation.id }))
      .then(request => {
        operationRequest.clear('staff_goods', operation.id, true)
        wx.showToast({ title: request.request_status === 'approved' ? '已直接生效' : '已提交审批', icon: 'success' })
        this.setData({ pendingGoodsRequest: null })
        this.resetForm()
        return this.loadGroupData(group.group_id, this.data.isTeamLeader, this.data.currentUserId)
      })
      .catch(err => this.showError(err, '提交申请失败'))
      .finally(() => this.setData({ saving: false }))
  },

  editGoods(event) {
    const id = Number(event.currentTarget.dataset.id)
    const item = this.data.goods.find(goods => goods.id === id)
    if (!item) {
      return
    }
    const statusIndex = item.status === 'off_sale' ? 1 : 0
    this.setData({
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
        status: item.status
      }, schedule.toForm(item.sale_starts_at)),
      statusIndex,
      selectedStatusLabel: statusOptions[statusIndex].label,
      submitTitle: this.data.isTeamLeader ? '直接保存修改' : '提交修改申请'
    })
  },

  deleteGoods(event) {
    const id = Number(event.currentTarget.dataset.id)
    const item = this.data.goods.find(goods => goods.id === id)
    const group = this.data.staffGroups[this.data.groupIndex]
    if (!item || !group) {
      return
    }
    wx.showModal({
      title: this.data.isTeamLeader ? '删除商品' : '申请删除商品',
      content: this.data.isTeamLeader ? `确认直接删除「${item.name}」吗？` : `负责人或管理员批准后「${item.name}」才会删除。`,
      confirmText: this.data.isTeamLeader ? '确认删除' : '提交申请',
      confirmColor: '#c04736',
      success: res => {
        if (!res.confirm) {
          return
        }
        const payload = {
          action: 'delete',
          group_id: group.group_id,
          goods_id: item.id
        }
        const operation = operationRequest.resolve('staff_goods', this.data.pendingGoodsRequest, payload, true)
        this.setData({ saving: true, pendingGoodsRequest: operation })
        staffGoods.submit(Object.assign({}, payload, { request_id: operation.id }))
          .then(request => {
            operationRequest.clear('staff_goods', operation.id, true)
            wx.showToast({ title: request.request_status === 'approved' ? '已删除' : '已提交审批', icon: 'success' })
            this.setData({ pendingGoodsRequest: null })
            return this.loadGroupData(group.group_id, this.data.isTeamLeader, this.data.currentUserId)
          })
          .catch(err => this.showError(err, '删除申请提交失败'))
          .finally(() => this.setData({ saving: false }))
      }
    })
  },

  resetForm() {
    this.setData({
      form: emptyForm(),
      statusIndex: 0,
      selectedStatusLabel: statusOptions[0].label,
      submitTitle: this.data.isTeamLeader ? '直接新增商品' : '提交新增申请'
    })
  },

  approveRequest(event) {
    const id = Number(event.currentTarget.dataset.id)
    const item = this.data.requests.find(request => request.id === id)
    if (!item || !item.can_review || this.data.saving) {
      return
    }
    wx.showModal({
      title: '批准商品申请',
      content: `批准后「${item.name_text}」将立即生效。`,
      confirmText: '批准',
      confirmColor: '#207f79',
      success: res => {
        if (res.confirm) {
          this.reviewRequest(id, true, '')
        }
      }
    })
  },

  rejectRequest(event) {
    const id = Number(event.currentTarget.dataset.id)
    const item = this.data.requests.find(request => request.id === id)
    if (!item || !item.can_review || this.data.saving) {
      return
    }
    wx.showModal({
      title: '驳回商品申请',
      editable: true,
      placeholderText: '填写至少 2 个字的驳回原因',
      confirmText: '确认驳回',
      confirmColor: '#c04736',
      success: res => {
        if (res.confirm) {
          const remark = (res.content || '').trim()
          if (remark.length < 2) {
            wx.showToast({ title: '请填写至少 2 个字的驳回原因', icon: 'none' })
            return
          }
          this.reviewRequest(id, false, remark)
        }
      }
    })
  },

  reviewRequest(id, approve, remark) {
    const group = this.data.staffGroups[this.data.groupIndex]
    if (!group) {
      return
    }
    this.setData({ saving: true, error: '' })
    const action = approve ? staffGoods.approve(id, remark) : staffGoods.reject(id, remark)
    action
      .then(() => {
        wx.showToast({ title: approve ? '已批准' : '已驳回', icon: 'success' })
        return this.loadGroupData(group.group_id, true, this.data.currentUserId)
      })
      .catch(err => this.showError(err, approve ? '批准失败' : '驳回失败'))
      .finally(() => this.setData({ saving: false }))
  },

  showError(err, fallback) {
    const message = err.message || fallback
    this.setData({ error: message })
    wx.showToast({ title: message, icon: 'none' })
  }
})
