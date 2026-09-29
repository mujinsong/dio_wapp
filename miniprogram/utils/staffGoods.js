const request = require('./request')

function requestPage(result) {
  if (result.page) {
    return result.page
  }
  return { items: result.requests || [], next_cursor: 0, has_more: false }
}

module.exports = {
  listGoods(groupId) {
    return request.get(`/staff/goods?group_id=${groupId}`).then(result => result.goods || [])
  },

  listRequests(groupId, cursor, limit) {
    return request.get(`/staff/goods/requests?group_id=${groupId}&cursor=${cursor || 0}&limit=${limit || 20}`).then(requestPage)
  },

  listTeamRequests(groupId, status, cursor, limit) {
    return request.get(`/team-leader/goods/requests?group_id=${groupId}&status=${encodeURIComponent(status || 'all')}&cursor=${cursor || 0}&limit=${limit || 20}`).then(requestPage)
  },

  submit(data) {
    return request.post('/staff/goods/requests', data).then(result => result.request)
  },

  approve(requestId, remark) {
    return request.post(`/team-leader/goods/requests/${requestId}/approve`, { remark: remark || '' }).then(result => result.request)
  },

  reject(requestId, remark) {
    return request.post(`/team-leader/goods/requests/${requestId}/reject`, { remark: remark || '' }).then(result => result.request)
  }
}
