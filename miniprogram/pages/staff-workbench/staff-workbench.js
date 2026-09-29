const auth = require('../../utils/auth')
const workbenchService = require('../../utils/staffWorkbench')

const statusNames = {
  approved: '已入账',
  pending: '待审批',
  rejected: '已驳回'
}

const paymentNames = {
  wechat: '微信支付',
  cash: '现金',
  alipay: '支付宝',
  card: '银行卡',
  other: '其他'
}

function formatDate(date) {
  const pad = value => String(value).padStart(2, '0')
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}`
}

function formatTime(raw) {
  const date = new Date(raw)
  if (!Number.isFinite(date.getTime())) {
    return '-'
  }
  const pad = value => String(value).padStart(2, '0')
  return `${pad(date.getHours())}:${pad(date.getMinutes())}`
}

function presentGrant(item) {
  const isTicket = item.grant_mode === 'ticket'
  return Object.assign({}, item, {
    time_text: formatTime(item.created_at),
    status_text: statusNames[item.request_status] || item.request_status,
    status_class: `record-status ${item.request_status || ''}`,
    title_text: isTicket ? `${item.ticket_unit_price} 元 × ${item.ticket_count} 张` : '手动积分',
    amount_text: isTicket ? `¥${item.total_amount}` : '',
    payment_text: isTicket ? (paymentNames[item.payment_method] || '其他') : '',
    user_text: item.user_nickname || `会员 #${item.user_id}`,
    correction_text: item.correction_status === 'pending' ? '冲正待审批' : (item.correction_status === 'approved' ? '已冲正' : ''),
    can_correct: item.request_status === 'approved' && !item.correction_status
  })
}

function presentRedeem(item) {
  return Object.assign({}, item, {
    time_text: formatTime(item.redeemed_at),
    user_text: item.user_nickname || `会员 #${item.user_id}`
  })
}

function presentPayments(items) {
  return (items || []).map(item => Object.assign({}, item, {
    method_text: paymentNames[item.payment_method] || '其他'
  }))
}

Page({
  data: {
    groups: [],
    groupIndex: 0,
    groupLabel: '',
    date: formatDate(new Date()),
    maxDate: formatDate(new Date()),
    minDate: formatDate(new Date(Date.now() - 180 * 24 * 60 * 60 * 1000)),
    summary: {},
    paymentSummary: [],
    grantRecords: [],
    redeemRecords: [],
    activeTab: 'grants',
    quotaPercent: 0,
    loaded: false,
    loading: true,
    error: ''
  },

  onLoad() {
    this.loadIdentity()
  },

  onShow() {
    if (this.data.loaded && !this.data.loading) {
      this.loadWorkbench()
    }
  },

  onPullDownRefresh() {
    this.loadWorkbench().finally(() => wx.stopPullDownRefresh())
  },

  loadIdentity() {
    this.setData({ loading: true, error: '' })
    return auth.me()
      .then(user => {
        const groups = Array.isArray(user.staff_groups) ? user.staff_groups : []
        if (!groups.length) {
          throw new Error('当前账号没有工作人员权限')
        }
        this.setGroup(groups, 0)
        return this.loadWorkbench()
      })
      .catch(err => this.showError(err, '工作人员信息加载失败'))
      .finally(() => this.setData({ loading: false, loaded: true }))
  },

  setGroup(groups, index) {
    const group = groups[index]
    this.setData({
      groups,
      groupIndex: index,
      groupLabel: group ? `${group.agency_name} · ${group.group_name}` : ''
    })
  },

  handleGroupChange(event) {
    this.setGroup(this.data.groups, Number(event.detail.value || 0))
    this.loadWorkbench()
  },

  handleDateChange(event) {
    this.setData({ date: event.detail.value })
    this.loadWorkbench()
  },

  loadWorkbench() {
    const group = this.data.groups[this.data.groupIndex]
    if (!group) {
      return Promise.resolve()
    }
    this.setData({ loading: true, error: '' })
    return workbenchService.get(group.agency_id, group.group_id, this.data.date)
      .then(workbench => {
        const summary = workbench.summary || {}
        const limit = Number(summary.daily_limit || 0)
        const used = Number(summary.daily_used || 0)
        const quotaPercent = limit > 0 ? Math.min(100, Math.max(0, Math.round(used * 100 / limit))) : 0
        this.setData({
          summary,
          quotaPercent,
          paymentSummary: presentPayments(workbench.payment_summary),
          grantRecords: (workbench.grant_records || []).map(presentGrant),
          redeemRecords: (workbench.redeem_records || []).map(presentRedeem)
        })
      })
      .catch(err => this.showError(err, '工作台数据加载失败'))
      .finally(() => this.setData({ loading: false }))
  },

  setTab(event) {
    const tab = event.currentTarget.dataset.tab
    if (tab === 'grants' || tab === 'redeems') {
      this.setData({ activeTab: tab })
    }
  },

  goAddPoints() {
    wx.navigateTo({ url: '/pages/staff-add-points/staff-add-points' })
  },

  goRedeem() {
    wx.navigateTo({ url: '/pages/staff-redeem-order/staff-redeem-order' })
  },

  requestCorrection(event) {
    const id = Number(event.currentTarget.dataset.id)
    const grant = this.data.grantRecords.find(item => item.id === id && item.can_correct)
    if (!grant) {
      return
    }
    const query = [
      `grant_id=${grant.id}`,
      `points=${grant.points}`,
      `sale_no=${encodeURIComponent(grant.sale_no || '')}`,
      `user_text=${encodeURIComponent(grant.user_text || '')}`
    ].join('&')
    wx.navigateTo({ url: `/pages/point-corrections/point-corrections?${query}` })
  },

  showError(err, fallback) {
    const messages = {
      staff_group_forbidden: '当前团体权限已失效',
      invalid_date: '只能查询最近 180 天记录'
    }
    const message = messages[err && err.code] || (err && err.message) || fallback
    this.setData({ error: message })
    wx.showToast({ title: message, icon: 'none' })
  }
})
