const request = require('./request')

function requestPage(result) {
  if (result.page) {
    return result.page
  }
  return { items: result.requests || [], next_cursor: 0, has_more: false, counts: {} }
}

module.exports = {
  listGroups(agencyId) {
    return request.get(`/admin/groups?agency_id=${agencyId}`).then(result => result.groups || [])
  },

  createGroup(data) {
    return request.post('/admin/groups', data).then(result => result.group)
  },

  updateGroup(groupId, data) {
    return request.put(`/admin/groups/${groupId}`, data).then(result => result.group)
  },

  deleteGroup(groupId) {
    return request.delete(`/admin/groups/${groupId}`, {}).then(result => result.ok)
  },

  listGoods(agencyId) {
    return request.get(`/admin/goods?agency_id=${agencyId}`).then(result => result.goods || [])
  },

  createGoods(data) {
    return request.post('/admin/goods', data).then(result => result.goods)
  },

  updateGoods(goodsId, data) {
    return request.put(`/admin/goods/${goodsId}`, data).then(result => result.goods)
  },

  deleteGoods(goodsId) {
    return request.delete(`/admin/goods/${goodsId}`, {}).then(result => result.ok)
  },

  listGoodsRequests(status, cursor, limit) {
    return request.get(`/admin/goods/requests?status=${encodeURIComponent(status || 'pending')}&cursor=${cursor || 0}&limit=${limit || 20}`).then(requestPage)
  },

  approveGoodsRequest(requestId, remark) {
    return request.post(`/admin/goods/requests/${requestId}/approve`, { remark: remark || '' }).then(result => result.request)
  },

  rejectGoodsRequest(requestId, remark) {
    return request.post(`/admin/goods/requests/${requestId}/reject`, { remark: remark || '' }).then(result => result.request)
  }
}
