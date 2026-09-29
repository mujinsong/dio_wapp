const config = require('./config')
const request = require('./request')
const session = require('./session')

let profileVersion = 0
let pendingLogin = null

function getLoginCode() {
  if (config.useMockLogin) {
    return Promise.resolve(`mock:${config.mockOpenid}`)
  }

  return new Promise((resolve, reject) => {
    wx.login({
      success(res) {
        if (res.code) {
          resolve(res.code)
          return
        }
        reject(new Error('微信登录失败'))
      },
      fail(err) {
        reject(err)
      }
    })
  })
}

function saveSession(result) {
  wx.setStorageSync('token', result.token)
  wx.setStorageSync('user', result.user)
  getApp().globalData.user = result.user
  return result.user
}

module.exports = {
  login() {
    if (pendingLogin) return pendingLogin
    session.invalidate()
    const snapshot = session.capture()
    const login = getLoginCode()
      .then(code => {
        session.assertCurrent(snapshot)
        return request.post('/auth/wechat-login', { code })
      })
      .then(result => {
        session.assertCurrent(snapshot)
        return saveSession(result)
      })
      .catch(err => {
        session.assertCurrent(snapshot)
        throw err
      })
      .finally(() => { if (pendingLogin === login) pendingLogin = null })
    pendingLogin = login
    return login
  },

  me() {
    const snapshot = session.capture()
    const profile = ++profileVersion
    return request.get('/auth/me')
      .then(result => {
        session.assertCurrent(snapshot)
        if (profile !== profileVersion) throw session.changedError()
        wx.setStorageSync('user', result.user)
        getApp().globalData.user = result.user
        return result.user
      })
      .catch(err => {
        session.assertCurrent(snapshot)
        if (profile !== profileVersion) throw session.changedError()
        throw err
      })
  },

  logout() {
    session.invalidate()
    pendingLogin = null
    wx.removeStorageSync('token')
    wx.removeStorageSync('user')
    getApp().globalData.user = null
  }
}
