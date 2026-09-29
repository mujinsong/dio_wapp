const auth = require('../../utils/auth')
const adminGoods = require('../../utils/adminGoods')
const points = require('../../utils/points')

const filters = [
  { label: '待审批', value: 'pending' },
  { label: '已批准', value: 'approved' },
  { label: '已驳回', value: 'rejected' },
  { label: '全部', value: 'all' }
]

const paymentText = {
  wechat: '微信支付',
  cash: '现金',
  alipay: '支付宝',
  card: '银行卡',
  other: '其他'
}

function formatTime(raw) {
  const date = new Date(raw)
  if (!Number.isFinite(date.getTime())) {
    return '-'
  }
  const pad = value => String(value).padStart(2, '0')
  return `${date.getMonth() + 1}-${pad(date.getDate())} ${pad(date.getHours())}:${pad(date.getMinutes())}`
}

function presentRequest(item) {
  const statusText = { pending: '待审批', approved: '已批准', rejected: '已驳回' }
  return Object.assign({}, item, {
    target_text: item.user_nickname || `会员 #${item.user_id}`,
    submitter_text: item.submitter_name || `工作人员 #${item.submitted_by}`,
    status_text: statusText[item.request_status] || item.request_status,
    status_class: `status ${item.request_status}`,
    created_text: formatTime(item.created_at),
    reviewed_text: item.reviewed_at ? formatTime(item.reviewed_at) : '',
    sale_text: item.grant_mode === 'ticket'
      ? `${item.ticket_unit_price} 元 × ${item.ticket_count} 张 · ${paymentText[item.payment_method] || item.payment_method}`
      : '手动积分发放',
    can_review: item.request_status === 'pending'
  })
}

function loadReviewGroups(user) {
  const leaderGroups = (user.staff_groups || []).filter(group => group.member_role === 'team_leader')
  const adminAgencies = user.admin_agencies || []
  return Promise.all(adminAgencies.map(agency => adminGoods.listGroups(agency.agency_id)
    .then(groups => groups
      .filter(group => group.status === 'normal')
      .map(group => ({
        agency_id: agency.agency_id,
        agency_name: agency.agency_name,
        group_id: group.id,
        group_name: group.name,
        member_role: 'admin'
      })))))
    .then(adminGroupLists => {
      const byGroup = {}
      leaderGroups.concat(...adminGroupLists).forEach(group => {
        byGroup[`${group.agency_id}:${group.group_id}`] = group
      })
      return Object.keys(byGroup).map(key => byGroup[key])
    })
}

Page({
  data: {
    reviewGroups: [],
    groupIndex: 0,
    selectedGroupLabel: '',
    filters,
    activeFilter: 'pending',
    requests: [],
    nextCursor: 0,
    hasMore: false,
    loading: true,
    reviewingId: 0,
    showEmpty: false,
    error: ''
  },

  onLoad() {
    this.loadIdentity()
  },

  onShow() {
    if (this.data.reviewGroups.length && !this.data.loading) {
      this.loadRequests()
    }
  },

  onPullDownRefresh() {
    this.loadRequests().finally(() => wx.stopPullDownRefresh())
  },

  onReachBottom() {
    return this.loadRequests(true)
  },

  retryLoad() {
    return this.loadRequests(this.data.requests.length > 0 && this.data.hasMore)
  },

  onUnload() {
    this._requestVersion = (this._requestVersion || 0) + 1
  },

  loadIdentity() {
    this.setData({ loading: true, error: '' })
    auth.me()
      .then(loadReviewGroups)
      .then(reviewGroups => {
        const group = reviewGroups[0]
        this.setData({
          reviewGroups,
          groupIndex: 0,
          selectedGroupLabel: group ? `${group.agency_name} · ${group.group_name}` : ''
        })
        return this.loadRequests()
      })
      .catch(err => {
        this.showError(err, '获取积分审批权限失败')
        this.setData({ loading: false })
      })
  },

  handleGroupChange(event) {
    const groupIndex = Number(event.detail.value || 0)
    const group = this.data.reviewGroups[groupIndex]
    this.setData({
      groupIndex,
      selectedGroupLabel: group ? `${group.agency_name} · ${group.group_name}` : ''
    })
    this.loadRequests()
  },

  handleFilter(event) {
    const activeFilter = event.currentTarget.dataset.value
    if (!filters.some(item => item.value === activeFilter) || activeFilter === this.data.activeFilter) {
      return
    }
    this.setData({ activeFilter, requests: [], showEmpty: false })
    this.loadRequests()
  },

  loadRequests(append = false) {
    if (append && (this.data.loading || !this.data.hasMore)) return Promise.resolve()
    const version = this._requestVersion = (this._requestVersion || 0) + 1
    const group = this.data.reviewGroups[this.data.groupIndex]
    if (!group) {
      this.setData({ requests: [], showEmpty: true, loading: false, nextCursor: 0, hasMore: false })
      return Promise.resolve()
    }
    if (!append) this.setData({ requests: [], nextCursor: 0, hasMore: false, showEmpty: false })
    this.setData({ loading: true, error: '' })
    return points.listPointGrantRequests(group.group_id, this.data.activeFilter, append ? this.data.nextCursor : 0, 20)
      .then(page => {
        if (version !== this._requestVersion) return
        const items = page.items.map(presentRequest)
        const requests = append ? this.data.requests.concat(items) : items
        this.setData({ requests, showEmpty: requests.length === 0, nextCursor: page.next_cursor, hasMore: page.has_more })
      })
      .catch(err => { if (version === this._requestVersion) this.showError(err, '积分申请加载失败') })
      .finally(() => { if (version === this._requestVersion) this.setData({ loading: false }) })
  },

  approve(event) {
    const request = this.findRequest(event)
    if (!request) {
      return
    }
    wx.showModal({
      title: '批准积分申请',
      content: `确认给 ${request.target_text} 增加 ${request.points} 积分吗？`,
      confirmText: '批准',
      success: result => {
        if (result.confirm) {
          this.performReview(request.id, true, '')
        }
      }
    })
  },

  reject(event) {
    const request = this.findRequest(event)
    if (!request) {
      return
    }
    wx.showModal({
      title: '驳回积分申请',
      editable: true,
      placeholderText: '填写驳回原因',
      confirmText: '确认驳回',
      confirmColor: '#b42318',
      success: result => {
        if (!result.confirm) {
          return
        }
        const remark = (result.content || '').trim()
        if (remark.length < 2) {
          wx.showToast({ title: '请填写至少 2 个字的驳回原因', icon: 'none' })
          return
        }
        this.performReview(request.id, false, remark)
      }
    })
  },

  findRequest(event) {
    const id = Number(event.currentTarget.dataset.id)
    return this.data.requests.find(item => item.id === id && item.can_review)
  },

  performReview(id, approve, remark) {
    if (this.data.reviewingId) {
      return
    }
    this.setData({ reviewingId: id, error: '' })
    const action = approve ? points.approvePointGrant(id, remark) : points.rejectPointGrant(id, remark)
    action
      .then(() => {
        wx.showToast({ title: approve ? '已批准并入账' : '已驳回', icon: approve ? 'success' : 'none' })
        return this.loadRequests()
      })
      .catch(err => this.showError(err, approve ? '批准失败' : '驳回失败'))
      .finally(() => this.setData({ reviewingId: 0 }))
  },

  showError(err, fallback) {
    const messages = {
      points_balance_limit: '会员积分余额已达上限，无法批准',
      grant_request_already_reviewed: '该申请已经处理',
      grant_submitter_inactive: '提交人已不再具备该团队权限，不能批准',
      point_grant_review_forbidden: '没有该团队的审批权限',
      invalid_review_remark: '驳回时请填写至少 2 个字的原因',
      user_disabled: '会员账户已停用'
    }
    const message = messages[err && err.code] || (err && err.message) || fallback
    this.setData({ error: message })
    wx.showToast({ title: message, icon: 'none' })
  }
})
