const request = require('./request')

module.exports = {
  list(agencyId) {
    return request.get(`/admin/staff-members?agency_id=${agencyId}`).then(result => result.members || [])
  },

  save(data) {
    return request.post('/admin/staff-members', data).then(result => result.member)
  },

  update(memberId, data) {
    return request.put(`/admin/staff-members/${memberId}`, data).then(result => result.member)
  },

  disable(memberId) {
    return request.delete(`/admin/staff-members/${memberId}`, {}).then(result => result.ok)
  }
}
