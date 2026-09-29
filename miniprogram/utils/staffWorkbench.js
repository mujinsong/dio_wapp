const request = require('./request')

module.exports = {
  get(agencyId, groupId, date) {
    const query = [
      `agency_id=${encodeURIComponent(agencyId)}`,
      `group_id=${encodeURIComponent(groupId)}`,
      `date=${encodeURIComponent(date)}`
    ].join('&')
    return request.get(`/staff/workbench?${query}`).then(result => result.workbench)
  }
}
