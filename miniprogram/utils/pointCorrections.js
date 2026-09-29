const request = require('./request')

module.exports = {
  listMine(status, cursor, limit) {
    return request.get(`/staff/points/corrections?status=${encodeURIComponent(status || 'all')}&cursor=${cursor || 0}&limit=${limit || 20}`)
      .then(result => result.page || { items: result.requests || [], next_cursor: 0, has_more: false })
  },

  submit(data) {
    return request.post('/staff/points/corrections', data).then(result => result.request)
  },

  listReview(groupId, status, cursor, limit) {
    return request.get(`/team-leader/points/corrections?group_id=${groupId}&status=${encodeURIComponent(status || 'pending')}&cursor=${cursor || 0}&limit=${limit || 20}`)
      .then(result => result.page || { items: result.requests || [], next_cursor: 0, has_more: false })
  },

  approve(id, remark) {
    return request.post(`/team-leader/points/corrections/${id}/approve`, { remark: remark || '' }).then(result => result.request)
  },

  reject(id, remark) {
    return request.post(`/team-leader/points/corrections/${id}/reject`, { remark: remark || '' }).then(result => result.request)
  }
}
