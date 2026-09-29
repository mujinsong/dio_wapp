const request = require('./request')

module.exports = {
  recoverExchange(goodsId, requestId) {
    return request.get(`/exchange/requests/${encodeURIComponent(requestId)}?goods_id=${encodeURIComponent(goodsId)}`).then(result => result.recovery)
  },
  listGroups() {
    return request.get('/shop/groups').then(result => result.groups || [])
  },

  listGoods(groupId) {
    const query = groupId ? `?group_id=${groupId}` : ''
    return request.get(`/goods${query}`).then(result => result.goods || [])
  },

  getGoods(goodsId) {
    return request.get(`/goods/${goodsId}`).then(result => result.goods)
  },

  exchangeGoods(goodsId, requestId, expectedPricePoints) {
    return request.post('/exchange/orders', { goods_id: goodsId, request_id: requestId, expected_price_points: expectedPricePoints }).then(result => result.result)
  },

  listOrders(cursor, limit) {
    const query = `?cursor=${cursor || 0}&limit=${limit || 20}`
    return request.get(`/exchange/orders${query}`).then(result => result.page || {
      items: result.orders || [],
      next_cursor: 0,
      has_more: false
    })
  },

  getOrder(orderId) {
    return request.get(`/exchange/orders/${orderId}`).then(result => result.order)
  },

  cancelOrder(orderId, requestId) {
    return request.post(`/exchange/orders/${orderId}/cancel`, { request_id: requestId }).then(result => result.result)
  },

  createRedeemQRCode(orderId) {
    return request.post(`/exchange/orders/${orderId}/redeem-qrcode`, {}).then(result => result.redeem_qr)
  },

  redeemOrder(redeemToken, requestId) {
    return request.post('/staff/exchange/orders/redeem', { redeem_token: redeemToken, request_id: requestId }).then(result => result.result)
  }
}
