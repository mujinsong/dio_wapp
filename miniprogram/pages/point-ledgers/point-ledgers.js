const auth = require('../../utils/auth')
const points = require('../../utils/points')

const typeNames = {
  staff_add: '现场积分入账',
  exchange_cost: '兑换商品',
  order_refund: '订单取消退款',
  admin_adjust: '管理员积分纠错',
  grant_reversal: '积分冲正'
}

function formatTime(raw) {
  const date = new Date(raw)
  if (!Number.isFinite(date.getTime())) {
    return '-'
  }
  const pad = value => String(value).padStart(2, '0')
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())} ${pad(date.getHours())}:${pad(date.getMinutes())}`
}

function presentLedger(item) {
  const delta = Number(item.delta_points || 0)
  return Object.assign({}, item, {
    title_text: typeNames[item.type] || '积分变动',
    group_text: item.group_name || item.agency_name || '事务所积分',
    remark_text: item.remark || '无备注',
    time_text: formatTime(item.created_at),
    delta_text: `${delta > 0 ? '+' : ''}${delta}`,
    delta_class: delta >= 0 ? 'ledger-delta positive' : 'ledger-delta negative',
    balance_text: `余额 ${item.after_points}`
  })
}

Page({
  data: {
    balance: 0,
    items: [],
    hasItems: false,
    cursor: 0,
    hasMore: false,
    loading: true,
    loadingMore: false,
    error: ''
  },

  onLoad() {
    this.refresh()
  },

  onPullDownRefresh() {
    this.refresh().finally(() => wx.stopPullDownRefresh())
  },

  onReachBottom() {
    return this.loadPage(false)
  },

  onUnload() {
    this._listVersion = (this._listVersion || 0) + 1
    this._balanceVersion = (this._balanceVersion || 0) + 1
  },

  refresh() {
    const version = this._balanceVersion = (this._balanceVersion || 0) + 1
    const cached = wx.getStorageSync('user') || {}
    this.setData({ balance: cached.points_balance || 0 })
    const userPromise = auth.me()
      .then(user => {
        if (version === this._balanceVersion) this.setData({ balance: user.points_balance || 0 })
      })
      .catch(() => null)
    return Promise.all([userPromise, this.loadPage(true)])
  },

  loadPage(reset) {
    if (!reset && (this.data.loading || this.data.loadingMore || !this.data.hasMore)) return Promise.resolve()
    const version = this._listVersion = (this._listVersion || 0) + 1
    if (reset) {
      this.setData({ loading: true, loadingMore: false, error: '', cursor: 0, hasMore: false, items: [], hasItems: false })
    } else {
      this.setData({ loadingMore: true, error: '' })
    }
    const cursor = reset ? 0 : this.data.cursor
    return points.listMyLedgers(cursor, 20)
      .then(page => {
        if (version !== this._listVersion) return
        const nextItems = (page.items || []).map(presentLedger)
        const items = reset ? nextItems : this.data.items.concat(nextItems)
        this.setData({
          items,
          hasItems: items.length > 0,
          cursor: page.next_cursor || 0,
          hasMore: !!page.has_more
        })
      })
      .catch(err => {
        if (version !== this._listVersion || err.code === 'session_changed') return
        const message = err.message || '积分明细加载失败'
        this.setData({ error: message })
        wx.showToast({ title: message, icon: 'none' })
      })
      .finally(() => {
        if (version === this._listVersion) this.setData({ loading: false, loadingMore: false })
      })
  }
})
