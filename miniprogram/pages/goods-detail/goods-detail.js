const shop = require('../../utils/shop')
const operationRequest = require('../../utils/operationRequest')
const session = require('../../utils/session')
const exchangeRecovery = require('../../utils/exchangeRecovery')

function presentGoods(goods, balance) {
  const soldOut = goods.sold_out || goods.stock <= 0
  const purchaseLimit = Number(goods.purchase_limit_per_user || 0)
  const exchangeCount = Number(goods.user_exchange_count || 0)
  const limitActive = !!goods.purchase_limit_active
  const limitReached = limitActive && (goods.limit_reached || exchangeCount >= purchaseLimit)
  const canBuy = !soldOut && !limitReached && balance >= goods.price_points
  return Object.assign({}, goods, {
    initial: (goods.name || '兑').slice(0, 1),
    has_image: !!goods.image_url,
    description_text: goods.description || '该商品暂时没有更多说明。',
    price_text: `${goods.price_points} PTS`,
    stock_text: soldOut ? '已兑完' : `剩余 ${goods.stock} 件`,
    has_limit: limitActive,
    limit_text: limitActive ? `上架后的前 ${goods.purchase_limit_hours} 小时，每人最多兑换 ${purchaseLimit} 件` : '',
    exchange_count_text: `你已兑换 ${exchangeCount} 件`,
    limit_reached: limitReached,
    can_buy: canBuy,
    disabled: !canBuy,
    button_text: soldOut ? '已兑完' : (limitReached ? '已达限购' : (canBuy ? '确认兑换' : '积分不足'))
  })
}

Page({
  data: {
    goodsId: 0,
    user: null,
    goods: null,
    balance: 0,
    hasGoods: false,
    loading: true,
    buying: false,
    pendingExchangeRequest: null,
    showInlineError: false,
    error: ''
  },

  onLoad(options) {
    const goodsId = Number(options.id)
    if (!goodsId) {
      this.setData({ loading: false, error: '商品参数不正确' })
      return
    }
    this.setData({ goodsId })
    this.loadDetail()
  },

  onPullDownRefresh() {
    this.loadDetail().finally(() => wx.stopPullDownRefresh())
  },

  onShow() {
    if (this.data.goods) {
      this.scheduleLimitRefresh(this.data.goods)
    }
  },

  onHide() {
    this.clearLimitTimer()
  },

  onUnload() {
    this._unloaded = true
    this._loadVersion = (this._loadVersion || 0) + 1
    this.clearLimitTimer()
  },

  loadDetail() {
    const version = this._loadVersion = (this._loadVersion || 0) + 1
    if (!this.data.goodsId) {
      return Promise.resolve()
    }
    this.clearLimitTimer()
    this.setData({ loading: true, showInlineError: false, error: '' })
    return shop.getGoods(this.data.goodsId)
      .then(goods => {
        if (this._unloaded || version !== this._loadVersion) return
        const balance = goods.points_balance || 0
        this.setData({
          balance,
          goods: presentGoods(goods, balance),
          hasGoods: true
        })
        this.scheduleLimitRefresh(goods)
        wx.setNavigationBarTitle({ title: goods.name || '商品详情' })
      })
      .catch(err => {
        if (this._unloaded || version !== this._loadVersion || err.code === 'session_changed') return
        const rawMessage = err.message || ''
        const message = rawMessage === 'goods not found'
          ? '商品不存在、已下架或所属团体已停用'
          : (rawMessage || '商品详情加载失败')
        this.setData({ error: message, hasGoods: false })
      })
      .finally(() => {
        if (this._unloaded || version !== this._loadVersion) return
        this.setData({ loading: false })
      })
  },

  scheduleLimitRefresh(goods) {
    if (!goods.purchase_limit_active || !goods.purchase_limit_ends_at) {
      return
    }
    const endsAt = Date.parse(goods.purchase_limit_ends_at)
    if (!Number.isFinite(endsAt) || endsAt <= Date.now()) {
      return
    }
    const delay = Math.min(endsAt - Date.now() + 500, 2147483000)
    this.limitTimer = setTimeout(() => this.loadDetail(), delay)
  },

  clearLimitTimer() {
    if (this.limitTimer) {
      clearTimeout(this.limitTimer)
      this.limitTimer = null
    }
  },

  exchange() {
    const goods = this.data.goods && Object.assign({}, this.data.goods)
    if (!goods || !goods.can_buy || this.data.buying) {
      return
    }
    const snapshot = session.capture()
    wx.showModal({
      title: '确认兑换',
      content: `${goods.group_name} · ${goods.name}\n将消耗 ${goods.price_points} 积分`,
      confirmText: '兑换',
      success: res => {
        if (res.confirm) {
          this.submitExchange(goods, snapshot)
        }
      }
    })
  },

  submitExchange(goods, snapshot = session.capture()) {
    if (this.data.buying || this._unloaded || !session.isCurrent(snapshot)) return Promise.resolve()
    const operation = operationRequest.resolve('exchange', this.data.pendingExchangeRequest, {
      goods_id: goods.id
    }, true)
    this.setData({
      buying: true,
      showInlineError: false,
      error: '',
      pendingExchangeRequest: operation
    })
    return shop.exchangeGoods(goods.id, operation.id, goods.price_points)
      .then(result => {
        session.assertCurrent(snapshot)
        operationRequest.clear('exchange', operation.id, true)
        if (this._unloaded) return
        const updatedGoods = Object.assign({}, goods, {
          stock: result.remaining_stock,
          sold_out: result.remaining_stock <= 0,
          user_exchange_count: result.user_exchange_count,
          limit_reached: goods.purchase_limit_active && goods.purchase_limit_per_user > 0 && result.user_exchange_count >= goods.purchase_limit_per_user,
          points_balance: result.after_points
        })
        this.setData({
          balance: result.after_points,
          goods: presentGoods(updatedGoods, result.after_points),
          pendingExchangeRequest: null
        })
        wx.showToast({ title: '兑换成功', icon: 'success' })
        if (result.order && result.order.id) {
          setTimeout(() => {
            if (this._unloaded || !session.isCurrent(snapshot)) return
            wx.navigateTo({ url: `/pages/exchange-order-detail/exchange-order-detail?id=${result.order.id}` })
          }, 500)
        }
      })
      .catch(err => {
        if (this._unloaded || err.code === 'session_changed') return
        if (err.code === 'operation_result_archived') return exchangeRecovery.recover(this, goods.id, operation, snapshot)
        if (err.code === 'goods_price_changed') {
          const message = '商品积分价格已变化，请按新价格重新确认兑换'
          wx.showToast({ title: message, icon: 'none' })
          return this.loadDetail().then(() => {
            if (!this._unloaded && session.isCurrent(snapshot)) this.setData({ error: message, showInlineError: true })
          })
        }
        const message = err.message === 'purchase limit reached'
          ? '已达到该商品限购数量'
          : (err.message === 'goods exchange is busy, please retry' ? '当前兑换人数较多，请稍后重试' : (err.message || '兑换失败'))
        this.setData({ error: message, showInlineError: true })
        wx.showToast({ title: message, icon: 'none' })
      })
      .finally(() => {
        if (this._unloaded) return
        this.setData({ buying: false })
      })
  },

  retry() {
    this.loadDetail()
  },

  goBack() {
    wx.navigateBack()
  }
})
