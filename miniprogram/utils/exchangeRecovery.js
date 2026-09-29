const shop = require('./shop')
const session = require('./session')
const operationRequest = require('./operationRequest')

function modal(options) {
  return new Promise((resolve, reject) => wx.showModal(Object.assign({}, options, { success: resolve, fail: reject })))
}

async function recover(page, goodsId, operation, snapshot) {
  const current = () => !page._unloaded && session.isCurrent(snapshot) &&
    page.data.pendingExchangeRequest && page.data.pendingExchangeRequest.id === operation.id
  try {
    const result = await shop.recoverExchange(goodsId, operation.id)
    if (!current() || !result.completed || result.request_id !== operation.id || result.goods_id !== goodsId) return
    const order = result.order
    const status = { pending: '待核销', redeemed: '已核销', canceled: '已取消', expired: '已过期' }
    const answer = await modal({
      title: '这笔兑换已处理',
      content: order ? `原订单 ${order.order_no}\n${order.goods_name} · ${order.points_cost} 积分 · ${status[order.status] || order.status}` : '历史响应已归档，无法直接定位原订单。请先到兑换记录核对，避免重复兑换。',
      confirmText: order ? '查看原订单' : '兑换记录',
      cancelText: '已核对'
    })
    if (!current()) return
    if (answer.confirm) {
      wx.navigateTo({ url: order ? `/pages/exchange-order-detail/exchange-order-detail?id=${order.id}` : '/pages/exchange-orders/exchange-orders' })
      return
    }
    if (!answer.cancel) return
    const reset = await modal({ title: '准备新的兑换', content: '确认已经核对原订单？继续后只会清除本地旧请求，不会扣积分。再次兑换将创建新订单。', confirmText: '确认已核对', cancelText: '暂不处理' })
    if (!current() || !reset.confirm) return
    operationRequest.clear('exchange', operation.id, true)
    page.setData({ pendingExchangeRequest: null, error: '', showInlineError: false })
    wx.showToast({ title: '已解除旧请求，请重新确认兑换', icon: 'none' })
  } catch (err) {
    if (!current() || err.code === 'session_changed') return
    page.setData({ error: err.message || '原订单查询失败，旧请求已保留', showInlineError: true })
  }
}

module.exports = { recover }
