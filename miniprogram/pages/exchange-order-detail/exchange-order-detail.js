const shop = require('../../utils/shop')
const operationRequest = require('../../utils/operationRequest')

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
    canceled_text: order.canceled_at ? formatTime(order.canceled_at) : '',
	expires_text: order.expires_at ? formatTime(order.expires_at) : '',
	expired_text: order.expired_at ? formatTime(order.expired_at) : '',
    points_text: `${order.points_cost || 0} PTS`,
    group_label: order.group_name || '默认团体',
    can_redeem: status === 'pending',
    can_cancel: status === 'pending',
	done_mark: status === 'redeemed' ? '✓' : '×',
	done_mark_class: status === 'redeemed' ? 'done-mark' : 'done-mark canceled',
	done_copy: status === 'expired'
	  ? '订单超过核销期限，积分已自动退回，商品库存已恢复'
	  : (status === 'canceled' ? '积分已原路退回，商品库存已恢复' : '订单已完成核销')
  })
}

Page({
  data: {
    orderId: 0,
    order: null,
    redeemQR: null,
    loading: true,
    qrLoading: false,
    canceling: false,
    pendingCancelRequest: null,
    hasOrder: false,
    error: ''
  },

  onLoad(options) {
    const orderId = Number(options.id)
    if (!orderId) {
      this.setData({ loading: false, error: '订单参数不正确' })
      return
    }
    this.setData({ orderId })
    this.loadDetail()
  },

  onPullDownRefresh() {
    this.loadDetail().finally(() => wx.stopPullDownRefresh())
  },

  onShow() {
    if (this.data.hasOrder && !this.data.loading) {
      this.loadDetail()
    }
  },

  loadDetail() {
    if (!this.data.orderId) {
      return Promise.resolve()
    }
    this.setData({ loading: true, error: '', redeemQR: null })
    return shop.getOrder(this.data.orderId)
      .then(order => {
        const presented = presentOrder(order)
        this.setData({
          order: presented,
          hasOrder: true
        })
        if (presented.can_redeem) {
          return this.loadRedeemQRCode()
        }
        return null
      })
      .catch(err => {
        const message = err.message || '订单加载失败'
        this.setData({ error: message, hasOrder: false })
        wx.showToast({ title: message, icon: 'none' })
      })
      .finally(() => {
        this.setData({ loading: false })
      })
  },

  loadRedeemQRCode() {
    if (!this.data.orderId || this.data.qrLoading) {
      return Promise.resolve()
    }
    this.setData({ qrLoading: true, error: '' })
    return shop.createRedeemQRCode(this.data.orderId)
      .then(qr => {
        this.setData({ redeemQR: qr })
      })
      .catch(err => {
        const message = err.message || '核销码生成失败'
        this.setData({ error: message })
        wx.showToast({ title: message, icon: 'none' })
      })
      .finally(() => {
        this.setData({ qrLoading: false })
      })
  },

  refreshQRCode() {
    this.loadRedeemQRCode()
  },

  cancelOrder() {
    if (!this.data.order || !this.data.order.can_cancel || this.data.canceling) {
      return
    }
    wx.showModal({
      title: '取消兑换',
      content: `确定取消“${this.data.order.goods_name}”吗？积分将退回账户。`,
      confirmText: '确认取消',
      confirmColor: '#b42318',
      success: result => {
        if (result.confirm) {
          this.submitCancellation()
        }
      }
    })
  },

  submitCancellation() {
    const operation = operationRequest.resolve('cancel', this.data.pendingCancelRequest, {
      order_id: this.data.orderId
    }, true)
    this.setData({ canceling: true, error: '', pendingCancelRequest: operation })
    shop.cancelOrder(this.data.orderId, operation.id)
      .then(result => {
        operationRequest.clear('cancel', operation.id, true)
        this.setData({
          order: presentOrder(result.order),
          redeemQR: null,
          pendingCancelRequest: null
        })
        wx.showToast({ title: `已退回 ${result.order.points_cost} 积分`, icon: 'none' })
      })
      .catch(err => {
        const messages = {
          order_already_redeemed: '订单已经核销，无法取消',
          order_not_cancelable: '当前订单无法取消',
		  order_expired: '订单已超过核销期限，积分会自动退回',
          goods_stock_limit: '商品库存已达上限，请联系工作人员处理',
          points_balance_limit: '积分余额已达上限，请联系工作人员处理'
        }
        const message = messages[err.code] || err.message || '取消订单失败'
        this.setData({ error: message })
        wx.showToast({ title: message, icon: 'none' })
        this.loadDetail()
      })
      .finally(() => {
        this.setData({ canceling: false })
      })
  },

  copyOrderNo() {
    const order = this.data.order
    if (!order || !order.order_no) {
      return
    }
    wx.setClipboardData({ data: order.order_no })
  }
})
