const request = require('./request')

module.exports = {
  reconcile(agencyId, kind, cursor) {
    return request.get(`/admin/reports/reconciliation?agency_id=${encodeURIComponent(agencyId)}&kind=${encodeURIComponent(kind)}&cursor=${cursor || 0}&limit=20`).then(result => result.page)
  },
  overview(agencyId, dateFrom, dateTo) {
    const query = [
      `agency_id=${encodeURIComponent(agencyId)}`,
      `date_from=${encodeURIComponent(dateFrom)}`,
      `date_to=${encodeURIComponent(dateTo)}`
    ].join('&')
    return request.get(`/admin/reports/overview?${query}`).then(result => result.report)
  }
}
