const auth = require('../../utils/auth')
const shop = require('../../utils/shop')
const operationRequest = require('../../utils/operationRequest')
const session = require('../../utils/session')
const exchangeRecovery = require('../../utils/exchangeRecovery')

function presentGoods(goods, balance) {
  return goods.map(item => {
    const soldOut = item.sold_out || item.stock <= 0
    const purchaseLimit = Number(item.purchase_limit_per_user || 0)
    const exchangeCount = Number(item.user_exchange_count || 0)
    const limitActive = !!item.purchase_limit_active
    const limitReached = limitActive && (item.limit_reached || exchangeCount >= purchaseLimit)
    const canBuy = !soldOut && !limitReached && balance >= item.price_points
    return Object.assign({}, item, {
      initial: (item.name || '兑').slice(0, 1),
      has_image: !!item.image_url,
      stock_text: soldOut ? '已兑完' : `剩余 ${item.stock}`,
      price_text: `${item.price_points} PTS`,
      group_label: item.group_name || '未命名团体',
      has_limit: limitActive,
      limit_text: limitActive ? `前 ${item.purchase_limit_hours} 小时限购 ${purchaseLimit} · 已兑 ${exchangeCount}` : '',
      limit_reached: limitReached,
      can_buy: canBuy,
      disabled: !canBuy,
      button_text: soldOut ? '已兑完' : (limitReached ? '已达限购' : (canBuy ? '兑换' : '积分不足'))
    })
  })
}

function presentGroupTabs(groups, activeGroupId) {
  const tabs = [{ id: 0, name: '全部', agency_name: '' }].concat(groups)
  return tabs.map(group => Object.assign({}, group, {
    tab_class: group.id === activeGroupId ? 'group-tab active' : 'group-tab'
  }))
}

Page({
  data: {
    user: null,
    balance: 0,
    groups: [],
    activeGroupId: 0,
    allGoods: [],
    goods: [],
    hasGoods: false,
    showEmpty: false,
    loading: false,
    buyingGoodsId: 0,
    pendingExchangeRequest: null,
    error: ''
  },

  onShow() {
    this.loadPage()
  },

  onHide() {
    this.clearLimitTimer()
  },

  onUnload() {
    this._unloaded = true
    this._loadVersion = (this._loadVersion || 0) + 1
    this.clearLimitTimer()
  },

  loadPage() {
  const version = this._loadVersion = (this._loadVersion || 0) + 1
  this.clearLimitTimer()
  this.setData({ loading: true, error: '' })
    return Promise.all([auth.me(), shop.listGroups(), shop.listGoods()])
      .then(([user, groups, goods]) => {
        if (this._unloaded || version !== this._loadVersion) return
        const balance = Number(user.points_balance || 0)
        this.setData({
          user,
          balance,
          groups: presentGroupTabs(groups, 0),
          activeGroupId: 0,
          allGoods: goods
        })
        this.applyGroup(0, goods, balance)
        this.scheduleLimitRefresh(goods)
      })
      .catch(err => {
        if (this._unloaded || version !== this._loadVersion || err.code === 'session_changed') return
        const message = err.message || '商店加载失败'
        this.setData({ error: message })
        wx.showToast({ title: message, icon: 'none' })
      })
      .finally(() => {
        if (this._unloaded || version !== this._loadVersion) return
        this.setData({ loading: false })
      })
  },

  selectGroup(event) {
    const groupId = Number(event.currentTarget.dataset.groupId || 0)
    this.setData({
      activeGroupId: groupId,
      groups: presentGroupTabs(this.data.groups.filter(group => group.id !== 0), groupId)
    })
    this.applyGroup(groupId, this.data.allGoods, this.data.balance)
  },

  applyGroup(groupId, allGoods, balance) {
    const filtered = groupId > 0
      ? allGoods.filter(item => item.group_id === groupId)
      : allGoods
    this.setData({
      goods: presentGoods(filtered, balance),
      hasGoods: filtered.length > 0,
      showEmpty: filtered.length === 0
    })
  },

  scheduleLimitRefresh(goods) {
    const endTimes = goods
      .filter(item => item.purchase_limit_active && item.purchase_limit_ends_at)
      .map(item => Date.parse(item.purchase_limit_ends_at))
      .filter(value => Number.isFinite(value) && value > Date.now())
    if (endTimes.length === 0) {
      return
    }
    const delay = Math.min(Math.min.apply(null, endTimes) - Date.now() + 500, 2147483000)
    this.limitTimer = setTimeout(() => this.loadPage(), delay)
  },

  clearLimitTimer() {
    if (this.limitTimer) {
      clearTimeout(this.limitTimer)
      this.limitTimer = null
    }
  },

  openGoods(event) {
    const goodsId = Number(event.currentTarget.dataset.goodsId)
    if (!goodsId) {
      return
    }
    wx.navigateTo({ url: `/pages/goods-detail/goods-detail?id=${goodsId}` })
  },

  exchange(event) {
  if (this.data.buyingGoodsId) {
    return
  }
    const goodsId = Number(event.currentTarget.dataset.goodsId)
    const item = this.data.goods.find(goods => goods.id === goodsId)
    if (!item || !item.can_buy) {
      return
    }

    const confirmedItem = Object.assign({}, item)
    const snapshot = session.capture()
    wx.showModal({
      title: '确认兑换',
      content: `${item.group_label} · ${item.name}\n消耗 ${item.price_points} 积分`,
      confirmText: '兑换',
      success: res => {
        if (res.confirm) {
          this.submitExchange(confirmedItem, snapshot)
        }
      }
    })
  },

  submitExchange(item, snapshot = session.capture()) {
    if (this.data.buyingGoodsId || this._unloaded || !session.isCurrent(snapshot)) return Promise.resolve()
    const payload = { goods_id: item.id }
    const operation = operationRequest.resolve('exchange', this.data.pendingExchangeRequest, payload, true)
    this.setData({
      buyingGoodsId: item.id,
      pendingExchangeRequest: operation,
      error: ''
    })
    return shop.exchangeGoods(item.id, operation.id, item.price_points)
      .then(result => {
        session.assertCurrent(snapshot)
        operationRequest.clear('exchange', operation.id, true)
        if (this._unloaded) return
        const user = Object.assign({}, this.data.user, { points_balance: result.after_points })
        const allGoods = this.data.allGoods.map(goods => {
          if (goods.id !== item.id) {
            return goods
          }
          return Object.assign({}, goods, {
            stock: result.remaining_stock,
            sold_out: result.remaining_stock <= 0,
            user_exchange_count: result.user_exchange_count,
            limit_reached: goods.purchase_limit_active && goods.purchase_limit_per_user > 0 && result.user_exchange_count >= goods.purchase_limit_per_user
          })
        })
        wx.setStorageSync('user', user)
        getApp().globalData.user = user
        this.setData({ user, balance: result.after_points, allGoods, pendingExchangeRequest: null })
        this.applyGroup(this.data.activeGroupId, allGoods, result.after_points)
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
        if (err.code === 'operation_result_archived') return exchangeRecovery.recover(this, item.id, operation, snapshot)
        if (err.code === 'goods_price_changed') {
          const message = '商品积分价格已变化，请按新价格重新确认兑换'
          wx.showToast({ title: message, icon: 'none' })
          return this.loadPage().then(() => {
            if (!this._unloaded && session.isCurrent(snapshot)) this.setData({ error: message })
          })
        }
        const message = err.message === 'purchase limit reached'
          ? '已达到该商品限购数量'
          : (err.message === 'goods exchange is busy, please retry' ? '当前兑换人数较多，请稍后重试' : (err.message || '兑换失败'))
        this.setData({ error: message })
        wx.showToast({ title: message, icon: 'none' })
      })
      .finally(() => {
        if (this._unloaded) return
        this.setData({ buyingGoodsId: 0 })
      })
  }
})
