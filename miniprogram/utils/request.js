const config = require('./config')
const session = require('./session')

function request(method, path, data) {
  const snapshot = session.capture()
  const token = snapshot.token

  return new Promise((resolve, reject) => {
    wx.request({
      url: `${config.baseURL}${path}`,
      method,
      data,
      timeout: config.requestTimeout || 15000,
      header: {
        'Content-Type': 'application/json',
        Authorization: token ? `Bearer ${token}` : ''
      },
      success(res) {
        if (!session.isCurrent(snapshot)) {
          reject(session.changedError())
          return
        }
        if (res.statusCode >= 200 && res.statusCode < 300) {
          resolve(res.data)
          return
        }

        const payload = res.data && res.data.error ? res.data.error : null
        const messages = {
          rate_limited: '操作过于频繁，请稍后再试',
          goods_price_changed: '商品积分价格已变化，请刷新后重新确认',
          invalid_expected_price: '请刷新商品页面后重新确认兑换价格',
          operation_result_archived: '这笔操作已经处理，详细响应已归档。请先核对订单或积分记录，勿重复提交',
          exchange_request_not_found: '未找到已完成的兑换，旧请求已保留。请稍后重试原兑换，勿另建重复请求',
          missing_token: '登录已失效，请重新登录',
          invalid_token: '登录已失效，请重新登录',
          user_disabled: '账户已停用'
        }
        let message = payload ? (messages[payload.code] || payload.message) : '请求失败'
        if (payload && payload.code === 'internal_error' && payload.request_id) {
          message = `${message}（编号 ${payload.request_id}）`
        }
        const error = new Error(message)
        error.code = payload ? payload.code : ''
        error.requestId = payload ? (payload.request_id || '') : ''
        reject(error)
      },
      fail(err) {
        if (!session.isCurrent(snapshot)) {
          reject(session.changedError())
          return
        }
        const detail = err && err.errMsg ? err.errMsg : ''
        if (detail.indexOf('timeout') >= 0) {
          reject(new Error('请求超时，可直接重试'))
          return
        }
        if (detail.indexOf('request:fail') >= 0) {
          reject(new Error('无法连接后端服务，请确认 API 已启动'))
          return
        }
        reject(new Error(detail || '网络请求失败'))
      }
    })
  })
}

module.exports = {
  get(path) {
    return request('GET', path)
  },
  post(path, data) {
    return request('POST', path, data)
  },
  put(path, data) {
    return request('PUT', path, data)
  },
  delete(path, data) {
    return request('DELETE', path, data)
  }
}
