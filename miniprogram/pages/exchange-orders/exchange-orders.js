const shop = require('../../utils/shop')
const operationRequest = require('../../utils/operationRequest')
const exchangeRecovery = require('../../utils/exchangeRecovery')
const session = require('../../utils/session')

const statusText = {
  pending: '待核销',
  redeemed: '已核销',
	canceled: '已取消',
	expired: '已过期'
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
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())} ${pad(date.getHours())}:${pad(date.getMinutes())}`
}

function presentOrder(order) {
  const status = order.status || 'pending'
  return Object.assign({}, order, {
    status_text: statusText[status] || status,
    status_class: `status-pill ${status}`,
    created_text: formatTime(order.created_at),
    redeemed_text: order.redeemed_at ? formatTime(order.redeemed_at) : '',
	expires_text: order.expires_at ? formatTime(order.expires_at) : '',
    points_text: `${order.points_cost || 0} PTS`,
    group_label: order.group_name || '默认团体'
  })
}

Page({
  data: {
    orders: [],
    pendingExchanges: [],
    pendingExchangeRequest: null,
    recovering: false,
    loading: true,
    loadingMore: false,
    cursor: 0,
    hasMore: false,
    showEmpty: false,
    error: ''
  },

  onLoad() {
    return this.loadOrders()
  },

  onShow() {
    const changed = this.syncSession()
    const loading = changed || !this.data.loading ? this.loadOrders() : Promise.resolve()
    this.loadPendingExchanges()
    return loading
  },

  syncSession() {
    if (this._pageSession && session.isCurrent(this._pageSession)) return false
    this._pageSession = session.capture()
    this._listVersion = (this._listVersion || 0) + 1
    this._recoveryVersion = (this._recoveryVersion || 0) + 1
    this.setData({ orders: [], pendingExchanges: [], pendingExchangeRequest: null,
      recovering: false, loading: false, loadingMore: false, cursor: 0, hasMore: false, showEmpty: false, error: '' })
    return true
  },

  onPullDownRefresh() {
    this.loadOrders(true).finally(() => wx.stopPullDownRefresh())
  },

  onReachBottom() {
    return this.loadOrders(false)
  },

  onUnload() {
    this._unloaded = true
    this._listVersion = (this._listVersion || 0) + 1
  },

  loadPendingExchanges() {
    this.syncSession()
    try {
      const pendingExchanges = operationRequest.list('exchange').flatMap(operation => {
        try {
          const payload = JSON.parse(operation.fingerprint)
          return Number.isSafeInteger(payload.goods_id) && payload.goods_id > 0 ? [Object.assign({}, operation, { goods_id: payload.goods_id })] : []
        } catch (_) { return [] }
      })
      this.setData({ pendingExchanges })
    } catch (_) {
      this.setData({ pendingExchanges: [], error: '本地兑换记录读取失败，请稍后重试，勿清除缓存' })
    }
  },

  async recoverExchange(event) {
    if (this.syncSession()) {
      this.loadPendingExchanges()
      return
    }
    if (this.data.recovering) return
    const pending = this.data.pendingExchanges.find(item => item.id === event.currentTarget.dataset.requestId)
    if (!pending) return
    const snapshot = session.capture()
    const version = this._recoveryVersion = (this._recoveryVersion || 0) + 1
    this.setData({ recovering: true })
    try {
      const operation = operationRequest.resolve('exchange', pending, { goods_id: pending.goods_id }, true)
      this.setData({ pendingExchangeRequest: operation })
      await exchangeRecovery.recover(this, pending.goods_id, operation, snapshot)
    } catch (_) {
      if (!this._unloaded && session.isCurrent(snapshot)) this.setData({ error: '本地请求保存失败，尚未发起核对，请稍后重试' })
    } finally {
      if (!this._unloaded && version === this._recoveryVersion && session.isCurrent(snapshot)) {
        this.setData({ recovering: false })
        this.loadPendingExchanges()
      }
    }
  },

  loadOrders(reset = true) {
    if (this._unloaded) return Promise.resolve()
    if (this.syncSession()) reset = true
    if (!reset && (this.data.loading || this.data.loadingMore || !this.data.hasMore)) return Promise.resolve()
    const snapshot = session.capture()
    const version = this._listVersion = (this._listVersion || 0) + 1
    const current = () => !this._unloaded && version === this._listVersion && session.isCurrent(snapshot)
    if (reset) {
      this.setData({ loading: true, loadingMore: false, error: '', cursor: 0, hasMore: false, orders: [], showEmpty: false })
    } else {
      this.setData({ loadingMore: true, error: '' })
    }
    const cursor = reset ? 0 : this.data.cursor
    return shop.listOrders(cursor, 20)
      .then(page => {
        if (!current()) return
        const nextOrders = (page.items || []).map(presentOrder)
        const presented = reset ? nextOrders : this.data.orders.concat(nextOrders)
        this.setData({
          orders: presented,
          showEmpty: presented.length === 0,
          cursor: page.next_cursor || 0,
          hasMore: !!page.has_more
        })
      })
      .catch(err => {
        if (!current() || err.code === 'session_changed') return
        const message = err.message || '订单加载失败'
        this.setData({ error: message })
        wx.showToast({ title: message, icon: 'none' })
      })
      .finally(() => {
        if (!current()) return
        this.setData({ loading: false, loadingMore: false })
      })
  },

  openOrder(event) {
    const orderId = event.currentTarget.dataset.orderId
    if (!orderId) {
      return
    }
    wx.navigateTo({ url: `/pages/exchange-order-detail/exchange-order-detail?id=${orderId}` })
  },

  goShop() {
    wx.navigateTo({ url: '/pages/shop/shop' })
  }
})
