// Runtime8 Fixture: product detail page logic (Scenario B)
Page({
  data: {
    productId: 42,
    productName: '测试商品 #42',
    entryKind: 'share',
    queryStr: 'id=42&puser=7',
    scene: 1007
  },

  onLoad(query) {
    const id = query.id || '42'
    const puser = query.puser || '7'
    this.setData({
      productId: id,
      queryStr: 'id=' + id + '&puser=' + puser,
      entryKind: query.puser ? 'share' : 'normal'
    })
  },

  // Local "首页" — scrolls to top, does NOT navigate
  scrollToTop() {
    wx.pageScrollTo({ scrollTop: 0 })
  },

  // Real "首页" — navigates to home page (reLaunch for clean page stack)
  goHome() {
    wx.reLaunch({ url: '/pages/home/index' })
  },

  // Simulate re-entry via reLaunch with prior query
  simulateReentry() {
    wx.reLaunch({
      url: '/pages/product/detail?id=42&puser=7'
    })
  }
})
