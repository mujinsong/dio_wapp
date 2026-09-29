const request = require('./request')

module.exports = {
  createIdentityQRCode() {
    return request.post('/users/me/identity-qrcode', {}).then(result => result.identity_qr)
  },

  listMyLedgers(cursor, limit) {
    const query = `?cursor=${cursor || 0}&limit=${limit || 20}`
    return request.get(`/users/me/point-ledgers${query}`).then(result => result.page)
  },

  staffAddPoints(data) {
    return request.post('/staff/points/add', data).then(result => result.result)
  },

  listPointGrantRequests(groupId, status, cursor, limit) {
    return request.get(`/team-leader/points/requests?group_id=${groupId}&status=${encodeURIComponent(status || 'pending')}&cursor=${cursor || 0}&limit=${limit || 20}`)
      .then(result => result.page || { items: result.requests || [], next_cursor: 0, has_more: false })
  },

  approvePointGrant(requestId, remark) {
    return request.post(`/team-leader/points/requests/${requestId}/approve`, { remark: remark || '' }).then(result => result.request)
  },

  rejectPointGrant(requestId, remark) {
    return request.post(`/team-leader/points/requests/${requestId}/reject`, { remark: remark || '' }).then(result => result.request)
  },

  getAdminPointUser(agencyId, userId) {
    return request.get(`/admin/points/users/${userId}?agency_id=${agencyId}`).then(result => result.user)
  },

  listAdminUserLedgers(agencyId, userId, cursor, limit) {
    const query = `?agency_id=${agencyId}&cursor=${cursor || 0}&limit=${limit || 20}`
    return request.get(`/admin/points/users/${userId}/ledgers${query}`).then(result => result.page)
  },

  adminAdjustPoints(data) {
    return request.post('/admin/points/adjust', data).then(result => result.result)
  }
}
