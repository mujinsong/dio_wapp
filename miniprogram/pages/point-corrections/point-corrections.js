const auth = require('../../utils/auth')
const adminGoods = require('../../utils/adminGoods')
const corrections = require('../../utils/pointCorrections')
const operationRequest = require('../../utils/operationRequest')

const statusNames = {
  pending: '待审批',
  approved: '已冲正',
  rejected: '已驳回'
}

function formatTime(raw) {
  const date = new Date(raw)
  if (!Number.isFinite(date.getTime())) {
    return '-'
  }
  const pad = value => String(value).padStart(2, '0')
  return `${pad(date.getMonth() + 1)}-${pad(date.getDate())} ${pad(date.getHours())}:${pad(date.getMinutes())}`
}

function safeDecode(value) {
  try {
    return decodeURIComponent(value || '')
  } catch (err) {
    return value || ''
  }
}

function presentRequest(item) {
  return Object.assign({}, item, {
    target_text: item.user_nickname || `会员 #${item.user_id}`,
    submitter_text: item.submitter_name || `工作人员 #${item.submitted_by}`,
    source_text: item.original_sale_no || `积分申请 #${item.original_grant_request_id}`,
    status_text: statusNames[item.request_status] || item.request_status,
    status_class: `correction-status ${item.request_status || ''}`,
    created_text: formatTime(item.created_at),
    original_text: formatTime(item.original_created_at),
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
    activeTab: 'mine',
    sourceGrant: null,
    reason: '',
    pendingSubmit: null,
    mineRequests: [],
    reviewRequests: [],
    nextCursor: 0,
    hasMore: false,
    reviewGroups: [],
    groupIndex: 0,
    selectedGroupLabel: '',
    canReview: false,
    loading: true,
    submitting: false,
    reviewingId: 0,
    error: ''
  },

  onLoad(options) {
    const grantId = Number(options && options.grant_id)
    if (Number.isSafeInteger(grantId) && grantId > 0) {
      const saleNo = safeDecode(options.sale_no)
      this.setData({
        sourceGrant: {
          id: grantId,
          points: Number(options.points || 0),
          sale_no: saleNo,
          source_text: saleNo || `积分申请 #${grantId}`,
          user_text: safeDecode(options.user_text)
        }
      })
    }
    this.loadIdentity()
  },

  onPullDownRefresh() {
    this.loadCurrent().finally(() => wx.stopPullDownRefresh())
  },

  onReachBottom() {
    return this.loadCurrent(true)
  },

  retryLoad() {
    const items = this.data.activeTab === 'review' ? this.data.reviewRequests : this.data.mineRequests
    return this.loadCurrent(items.length > 0 && this.data.hasMore)
  },

  onUnload() {
    this._requestVersion = (this._requestVersion || 0) + 1
  },

  loadIdentity() {
    this.setData({ loading: true, error: '' })
    return auth.me()
      .then(user => loadReviewGroups(user))
      .then(reviewGroups => {
        const group = reviewGroups[0]
        this.setData({
          reviewGroups,
          canReview: reviewGroups.length > 0,
          groupIndex: 0,
          selectedGroupLabel: group ? `${group.agency_name} · ${group.group_name}` : ''
        })
        return this.loadCurrent()
      })
      .catch(err => {
        this.showError(err, '冲正信息加载失败')
        this.setData({ loading: false })
      })
  },

  setTab(event) {
    const activeTab = event.currentTarget.dataset.tab
    if (activeTab !== 'mine' && activeTab !== 'review') {
      return
    }
    this.setData({ activeTab, error: '' })
    this.loadCurrent()
  },

  loadCurrent(append = false) {
    return this.loadList(this.data.activeTab, append)
  },

  loadMine() {
    return this.loadList('mine')
  },

  loadReview() {
    return this.loadList('review')
  },

  loadList(tab, append = false) {
    if (tab !== this.data.activeTab || (append && (this.data.loading || !this.data.hasMore))) return Promise.resolve()
    const version = this._requestVersion = (this._requestVersion || 0) + 1
    const key = tab === 'review' ? 'reviewRequests' : 'mineRequests'
    const group = this.data.reviewGroups[this.data.groupIndex]
    if (!append) this.setData({ [key]: [], nextCursor: 0, hasMore: false })
    if (tab === 'review' && !group) {
      this.setData({ loading: false })
      return Promise.resolve()
    }
    this.setData({ loading: true, error: '' })
    const cursor = append ? this.data.nextCursor : 0
    const action = tab === 'review' ? corrections.listReview(group.group_id, 'pending', cursor, 20) : corrections.listMine('all', cursor, 20)
    return action
      .then(page => {
        if (version !== this._requestVersion) return
        const items = page.items.map(presentRequest)
        this.setData({ [key]: append ? this.data[key].concat(items) : items, nextCursor: page.next_cursor, hasMore: page.has_more })
      })
      .catch(err => { if (version === this._requestVersion) this.showError(err, '冲正申请加载失败') })
      .finally(() => { if (version === this._requestVersion) this.setData({ loading: false }) })
  },

  handleGroupChange(event) {
    const groupIndex = Number(event.detail.value || 0)
    const group = this.data.reviewGroups[groupIndex]
    this.setData({
      groupIndex,
      selectedGroupLabel: group ? `${group.agency_name} · ${group.group_name}` : ''
    })
    this.loadReview()
  },

  handleReason(event) {
    this.setData({ reason: event.detail.value || '' })
  },

  cancelSource() {
    this.setData({ sourceGrant: null, reason: '', pendingSubmit: null })
  },

  submitCorrection() {
    const source = this.data.sourceGrant
    const reason = this.data.reason.trim()
    if (!source || this.data.submitting) {
      return
    }
    if (reason.length < 2 || reason.length > 512) {
      wx.showToast({ title: '请填写 2 至 512 字冲正原因', icon: 'none' })
      return
    }
    wx.showModal({
      title: '提交积分冲正',
      content: `将申请扣回 ${source.points} 积分，提交后需负责人或管理员审批。`,
      confirmText: '提交申请',
      success: result => {
        if (result.confirm) {
          this.performSubmit(source, reason)
        }
      }
    })
  },

  performSubmit(source, reason) {
    const payload = { original_grant_request_id: source.id, reason }
    const operation = operationRequest.resolve('point_correction', this.data.pendingSubmit, payload, true)
    this.setData({ submitting: true, error: '', pendingSubmit: operation })
    corrections.submit(Object.assign({}, payload, { request_id: operation.id }))
      .then(() => {
        operationRequest.clear('point_correction', operation.id, true)
        this.setData({ sourceGrant: null, reason: '', pendingSubmit: null })
        wx.showToast({ title: '冲正申请已提交', icon: 'success' })
        return this.loadCurrent()
      })
      .catch(err => this.showError(err, '冲正申请提交失败'))
      .finally(() => this.setData({ submitting: false }))
  },

  approve(event) {
    const request = this.findReviewRequest(event)
    if (!request) {
      return
    }
    wx.showModal({
      title: '批准积分冲正',
      content: `确认从 ${request.target_text} 扣回 ${request.points} 积分吗？原记录会保留。`,
      confirmText: '批准冲正',
      confirmColor: '#b42318',
      success: result => {
        if (result.confirm) {
          this.performReview(request.id, true, '')
        }
      }
    })
  },

  reject(event) {
    const request = this.findReviewRequest(event)
    if (!request) {
      return
    }
    wx.showModal({
      title: '驳回冲正申请',
      editable: true,
      placeholderText: '填写驳回原因',
      confirmText: '确认驳回',
      success: result => {
        const remark = (result.content || '').trim()
        if (result.confirm && remark) {
          this.performReview(request.id, false, remark)
        } else if (result.confirm) {
          wx.showToast({ title: '请填写驳回原因', icon: 'none' })
        }
      }
    })
  },

  findReviewRequest(event) {
    const id = Number(event.currentTarget.dataset.id)
    return this.data.reviewRequests.find(item => item.id === id && item.can_review)
  },

  performReview(id, approve, remark) {
    if (this.data.reviewingId) {
      return
    }
    this.setData({ reviewingId: id, error: '' })
    const action = approve ? corrections.approve(id, remark) : corrections.reject(id, remark)
    action
      .then(() => {
        wx.showToast({ title: approve ? '积分已冲正' : '申请已驳回', icon: approve ? 'success' : 'none' })
        return this.loadCurrent()
      })
      .catch(err => this.showError(err, approve ? '冲正审批失败' : '驳回失败'))
      .finally(() => this.setData({ reviewingId: 0 }))
  },

  showError(err, fallback) {
    const messages = {
      correction_already_exists: '该积分记录已有待处理或已批准的冲正',
      grant_not_correctable: '只有已入账积分可以申请冲正',
      correction_insufficient_points: '会员当前积分不足，暂时无法冲正',
      cannot_review_own_request: '不能审批自己提交的冲正申请',
      point_grant_review_forbidden: '没有该团体的审批权限',
      invalid_review_remark: '驳回时请填写至少 2 个字的原因',
      correction_already_reviewed: '该冲正申请已经处理'
    }
    const message = messages[err && err.code] || (err && err.message) || fallback
    this.setData({ error: message })
    wx.showToast({ title: message, icon: 'none' })
  }
})
