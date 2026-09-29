const request = require('./request')
const config = require('./config')

function queryString(params) {
  return Object.keys(params)
    .filter(key => params[key] !== undefined && params[key] !== null && params[key] !== '')
    .map(key => `${encodeURIComponent(key)}=${encodeURIComponent(params[key])}`)
    .join('&')
}

function list(type, params) {
  return request.get(`/admin/operations/${type}?${queryString(params)}`).then(result => result.page)
}

module.exports = {
  listSales(params) {
    return list('sales', params)
  },

  listOrders(params) {
    return list('orders', params)
  },

  downloadCSV(type, params) {
    const token = wx.getStorageSync('token')
    const url = `${config.baseURL}/admin/operations/export?${queryString(Object.assign({}, params, { type }))}`
    return new Promise((resolve, reject) => {
      wx.downloadFile({
        url,
        timeout: config.requestTimeout || 15000,
        header: { Authorization: token ? `Bearer ${token}` : '' },
        success(res) {
          if (res.statusCode >= 200 && res.statusCode < 300 && res.tempFilePath) {
            resolve(res.tempFilePath)
            return
          }
          reject(new Error(res.statusCode === 400 ? '导出范围过大，请缩短日期后重试' : '导出失败'))
        },
        fail(err) {
          reject(new Error((err && err.errMsg) || '导出下载失败'))
        }
      })
    })
  }
}
