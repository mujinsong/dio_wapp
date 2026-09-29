const auth = require('../../utils/auth')

function presentUser(user) {
  if (!user) {
    return {}
  }

  const nickname = user.nickname || '微信用户'
  return Object.assign({}, user, {
    avatar_text: nickname.slice(0, 1),
    nickname_display: nickname,
    member_no: user.id || '-',
    points_balance_display: user.points_balance || 0,
    status_text: user.status === 'normal' ? '正常' : (user.status || '-')
  })
}

Page({
  data: {
    user: presentUser({})
  },

  onShow() {
    const cachedUser = wx.getStorageSync('user')
    if (cachedUser) {
      this.setData({ user: presentUser(cachedUser) })
    }

    if (wx.getStorageSync('token')) {
      auth.me().then(user => {
        this.setData({ user: presentUser(user) })
      })
    }
  }
})
