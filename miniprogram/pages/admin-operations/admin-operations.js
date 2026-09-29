const auth = require('../../utils/auth')
const operations = require('../../utils/adminOperations')

const saleStatuses = [
  { label: '全部状态', value: 'all' },
  { label: '已入账', value: 'approved' },
  { label: '待审批', value: 'pending' },
  { label: '已驳回', value: 'rejected' }
]

const orderStatuses = [
  { label: '全部状态', value: 'all' },
  { label: '待核销', value: 'pending' },
  { label: '已核销', value: 'redeemed' },
  { label: '已取消', value: 'canceled' },
  { label: '已过期', value: 'expired' }
]

const saleStatusNames = {
  approved: '已入账',
  pending: '待审批',
  rejected: '已驳回'
}

const orderStatusNames = {
  pending: '待核销',
  redeemed: '已核销',
  canceled: '已取消',
  expired: '已过期'
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

function defaultRange() {
  const end = new Date()
  const start = new Date(end.getFullYear(), end.getMonth(), end.getDate() - 6)
  return { dateFrom: formatDate(start), dateTo: formatDate(end), maxDate: formatDate(end) }
}

function formatTime(raw) {
  if (!raw) {
    return '-'
  }
  const date = new Date(raw)
  if (!Number.isFinite(date.getTime())) {
    return '-'
  }
  const pad = value => String(value).padStart(2, '0')
  return `${pad(date.getMonth() + 1)}-${pad(date.getDate())} ${pad(date.getHours())}:${pad(date.getMinutes())}`
}

function presentSale(item) {
  const ticket = item.grant_mode === 'ticket'
  return Object.assign({}, item, {
    status_text: saleStatusNames[item.request_status] || item.request_status,
    status_class: `status-pill ${item.request_status || ''}`,
    time_text: formatTime(item.created_at),
    business_text: ticket ? `${item.ticket_unit_price} 元 × ${item.ticket_count} 张` : '手动积分',
    payment_text: ticket ? (paymentNames[item.payment_method] || '其他') : '积分录入',
    amount_text: ticket ? `¥${item.total_amount}` : '',
    user_text: item.user_nickname || `会员 #${item.user_id}`,
    staff_text: item.submitter_name || `工作人员 #${item.submitted_by}`
  })
}

function presentOrder(item) {
  return Object.assign({}, item, {
    status_text: orderStatusNames[item.status] || item.status,
    status_class: `status-pill ${item.status || ''}`,
    time_text: formatTime(item.created_at),
    action_time_text: item.redeemed_at ? `核销 ${formatTime(item.redeemed_at)}` : (item.canceled_at ? `取消 ${formatTime(item.canceled_at)}` : (item.expired_at ? `过期 ${formatTime(item.expired_at)}` : '')),
    user_text: item.user_nickname || `会员 #${item.user_id}`,
    redeemer_text: item.redeemer_name ? ` · ${item.redeemer_name}` : ''
  })
}

Page({
  data: Object.assign({
    agencies: [],
    agencyIndex: 0,
    agencyId: 0,
    agencyName: '',
    activeType: 'sales',
    statusOptions: saleStatuses,
    statusIndex: 0,
    statusLabel: saleStatuses[0].label,
    keyword: '',
    items: [],
    cursor: 0,
    hasMore: false,
    loading: true,
    loadingMore: false,
    exporting: false,
    loaded: false,
    error: ''
  }, defaultRange()),

  onLoad() {
    this.loadAdmin()
  },

  onPullDownRefresh() {
    this.loadItems(true).finally(() => wx.stopPullDownRefresh())
  },

  onReachBottom() {
    if (this.data.hasMore && !this.data.loadingMore) {
      this.loadItems(false)
    }
  },

  loadAdmin() {
    this.setData({ loading: true, error: '' })
    return auth.me()
      .then(user => {
        const agencies = Array.isArray(user.admin_agencies) ? user.admin_agencies : []
        const agency = agencies[0]
        if (!agency) {
          throw new Error('当前账号没有管理员权限')
        }
        this.setData({
          agencies,
          agencyId: agency.agency_id,
          agencyName: agency.agency_name,
          agencyIndex: 0
        })
        return this.loadItems(true)
      })
      .catch(err => this.showError(err, '管理员权限加载失败'))
      .finally(() => this.setData({ loading: false, loaded: true }))
  },

  setType(event) {
    const activeType = event.currentTarget.dataset.type
    if (activeType !== 'sales' && activeType !== 'orders') {
      return
    }
    this.setData({
      activeType,
      statusOptions: activeType === 'sales' ? saleStatuses : orderStatuses,
      statusIndex: 0,
      statusLabel: (activeType === 'sales' ? saleStatuses : orderStatuses)[0].label,
      items: [],
      cursor: 0,
      hasMore: false
    })
    this.loadItems(true)
  },

  handleAgencyChange(event) {
    const agencyIndex = Number(event.detail.value || 0)
    const agency = this.data.agencies[agencyIndex]
    this.setData({
      agencyIndex,
      agencyId: agency ? agency.agency_id : 0,
      agencyName: agency ? agency.agency_name : ''
    })
    this.loadItems(true)
  },

  handleDateFrom(event) {
    this.setData({ dateFrom: event.detail.value })
  },

  handleDateTo(event) {
    this.setData({ dateTo: event.detail.value })
  },

  handleStatusChange(event) {
    const statusIndex = Number(event.detail.value || 0)
    const selected = this.data.statusOptions[statusIndex] || this.data.statusOptions[0]
    this.setData({ statusIndex, statusLabel: selected.label })
  },

  handleKeyword(event) {
    this.setData({ keyword: event.detail.value || '' })
  },

  applyFilters() {
    if (this.data.dateFrom > this.data.dateTo) {
      wx.showToast({ title: '开始日期不能晚于结束日期', icon: 'none' })
      return
    }
    this.loadItems(true)
  },

  currentParams(cursor) {
    const status = this.data.statusOptions[this.data.statusIndex] || this.data.statusOptions[0]
    return {
      agency_id: this.data.agencyId,
      date_from: this.data.dateFrom,
      date_to: this.data.dateTo,
      status: status.value,
      keyword: this.data.keyword.trim(),
      cursor: cursor || 0,
      limit: 20
    }
  },

  loadItems(reset) {
    if (!this.data.agencyId) {
      return Promise.resolve()
    }
    const cursor = reset ? 0 : this.data.cursor
    this.setData(reset ? { loading: true, error: '' } : { loadingMore: true, error: '' })
    const action = this.data.activeType === 'sales'
      ? operations.listSales(this.currentParams(cursor))
      : operations.listOrders(this.currentParams(cursor))
    return action
      .then(page => {
        const next = (page.items || []).map(this.data.activeType === 'sales' ? presentSale : presentOrder)
        this.setData({
          items: reset ? next : this.data.items.concat(next),
          cursor: page.next_cursor || 0,
          hasMore: !!page.has_more
        })
      })
      .catch(err => this.showError(err, '业务明细加载失败'))
      .finally(() => this.setData({ loading: false, loadingMore: false }))
  },

  exportCSV() {
    if (this.data.exporting || !this.data.agencyId) {
      return
    }
    this.setData({ exporting: true, error: '' })
    const params = this.currentParams(0)
    delete params.cursor
    delete params.limit
    operations.downloadCSV(this.data.activeType, params)
      .then(filePath => {
        if (typeof wx.shareFileMessage !== 'function') {
          throw new Error('当前微信版本不支持文件分享')
        }
        wx.shareFileMessage({
          filePath,
          fileName: `DIO-${this.data.activeType}-${this.data.dateFrom}-${this.data.dateTo}.csv`,
          fail: err => {
            if (!err || String(err.errMsg || '').indexOf('cancel') < 0) {
              this.showError(err, '文件分享失败')
            }
          }
        })
      })
      .catch(err => this.showError(err, '导出失败'))
      .finally(() => this.setData({ exporting: false }))
  },

  showError(err, fallback) {
    const messages = {
      admin_forbidden: '没有该事务所的管理员权限',
      invalid_date_range: '日期范围应为 1 至 90 天',
      export_too_large: '数据超过 5000 条，请缩短日期范围'
    }
    const message = messages[err && err.code] || (err && (err.message || err.errMsg)) || fallback
    this.setData({ error: message })
    wx.showToast({ title: message, icon: 'none' })
  }
})
