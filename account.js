(() => {
  const state = {user: null, plans: [], paymentProducts: {provider: 'mock', plans: [], rechargePackages: []}, records: [], orders: [], adminUsers: [], pricing: null, assetKind: 'all', assetPage: 1};
  let widget, dialog, message, lastUserID;
  let unlockInProgress = false;
  const sessionChannel = typeof BroadcastChannel === 'function' ? new BroadcastChannel('osh-session') : null;

  async function api(path, options = {}) {
    const response = await fetch(path, {
      credentials: 'same-origin',
      headers: options.body ? {'Content-Type': 'application/json'} : undefined,
      ...options,
      body: options.body ? JSON.stringify(options.body) : undefined
    });
    const data = await response.json().catch(() => ({}));
    if (!response.ok) {
      const err = new Error(data.message || '操作失败');
      err.status = response.status;
      err.retryAfter = Number(response.headers.get('Retry-After')) || 0;
      throw err;
    }
    return data;
  }

  const fmt = value => Number(value || 0).toLocaleString('zh-CN', {maximumFractionDigits: 2});
  const money = cents => `¥${(Number(cents || 0) / 100).toFixed(2)}`;
  const levelName = level => level >= 3 ? '管理员' : level >= 2 ? 'VIP' : '普通用户';
  const esc = value => String(value ?? '').replace(/[&<>"']/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
  const workspace = document.getElementById('workspaceBody');
  let assetRequest = 0;

  function setMessage(text, type = '') {
    if (!message) return;
    message.textContent = text || '';
    message.className = `accountMessage ${type}`;
  }

  function drawWidget() {
    if (!widget) return;
    const userID = state.user?.id || null;
    if (lastUserID !== userID) {
      lastUserID = userID;
      window.dispatchEvent(new CustomEvent('osh-user-change'));
    }
    widget.replaceChildren();
    const button = document.createElement('button');
    button.type = 'button';
    button.className = 'accountButton';
    button.innerHTML = state.user
      ? `<strong>${esc(state.user.username)}</strong><span>${levelName(state.user.level)} · ${fmt(state.user.quotaPoints)} 额度</span>`
      : '<strong>登录 / 注册</strong><span>我的创作空间</span>';
    button.addEventListener('click', openDialog);
    widget.append(button);
    window.OshShell?.updateUser(state.user);
  }

  function openDialog() {
    if (state.user) { location.href = '/account'; return; }
    renderDialog();
    if (dialog.showModal) dialog.showModal();
    else dialog.setAttribute('open', '');
  }

  function closeDialog() {
    state.notice = '';
    if (dialog.open && dialog.close) dialog.close();
    else dialog.removeAttribute('open');
    message = document.getElementById('workspaceMessage');
  }

  function renderDialog() {
    dialog.innerHTML = '';
    const shell = document.createElement('div');
    shell.className = 'accountShell';
    shell.innerHTML = '<div class="accountHead"><strong>OSH 灵感库</strong><button type="button" aria-label="关闭" title="关闭"><i data-lucide="x"></i></button></div><p class="accountMessage" role="status"></p><div class="accountBody"></div>';
    shell.querySelector('.accountHead button').addEventListener('click', closeDialog);
    message = shell.querySelector('.accountMessage');
    dialog.append(shell);
    renderAuth(shell.querySelector('.accountBody'));
    window.OshShell?.icons();
    if (state.notice) setMessage(state.notice, 'error');
  }

  function renderAuth(body) {
    body.innerHTML = `
      <nav class="authTabs" aria-label="账号操作"><button type="button" data-auth="loginForm" class="active">登录</button><button type="button" data-auth="registerForm">注册</button><button type="button" data-auth="resetForm">忘记密码</button></nav>
      <div class="authGrid">
        <form id="loginForm" class="accountForm">
          <h3>登录</h3>
          <label>账号<input name="login" autocomplete="username" placeholder="用户名或邮箱"></label>
          <label>密码<input name="password" type="password" autocomplete="current-password"></label>
          <button type="submit">登录</button>
        </form>
        <form id="registerForm" class="accountForm" hidden>
          <h3>注册</h3>
          <label>用户名<input name="username" autocomplete="username"></label>
          <label>邮箱<input name="email" type="email" autocomplete="email" required maxlength="254"></label>
          <div class="inlineFields"><label>验证码<input name="code" inputmode="numeric" autocomplete="one-time-code" required pattern="[0-9]{6}" maxlength="6"></label><button type="button" data-code="register">获取验证码</button></div>
          <label>密码<input name="password" type="password" autocomplete="new-password" required minlength="8" maxlength="128"></label>
          <label>确认密码<input name="confirm_password" type="password" autocomplete="new-password" required minlength="8" maxlength="128"></label>
          <button type="submit">注册并领取 888 充值积分</button>
        </form>
        <form id="resetForm" class="accountForm" hidden>
          <h3>忘记密码</h3>
          <label>邮箱<input name="email" type="email" autocomplete="email" required maxlength="254"></label>
          <div class="inlineFields"><label>验证码<input name="code" inputmode="numeric" autocomplete="one-time-code" required pattern="[0-9]{6}" maxlength="6"></label><button type="button" data-code="reset_password">获取验证码</button></div>
          <label>新密码<input name="password" type="password" autocomplete="new-password" required minlength="8" maxlength="128"></label>
          <label>确认新密码<input name="confirm_password" type="password" autocomplete="new-password" required minlength="8" maxlength="128"></label>
          <button type="submit">重置密码</button>
        </form>
      </div>`;
    for (const button of body.querySelectorAll('[data-auth]')) button.addEventListener('click', () => {
      body.querySelectorAll('.authGrid form').forEach(form => form.hidden = form.id !== button.dataset.auth);
      body.querySelectorAll('[data-auth]').forEach(tab => tab.classList.toggle('active', tab === button));
      setMessage('');
    });
    body.querySelector('#loginForm').addEventListener('submit', submitLogin);
    body.querySelector('#registerForm').addEventListener('submit', submitRegister);
    body.querySelector('#resetForm').addEventListener('submit', submitReset);
    for (const button of body.querySelectorAll('[data-code]')) {
      button.addEventListener('click', requestCode);
    }
  }

  async function requestCode(event) {
    const button = event.currentTarget;
    const form = button.closest('form');
    if (button.disabled || !form.elements.email.reportValidity()) return;
    const email = form.elements.email.value.trim();
    const purpose = button.dataset.code;
    button.disabled = true;
    button.textContent = '发送中...';
    let retryAfter = 0;
    try {
      const data = await api('/api/auth/email-code', {method: 'POST', body: {email, purpose}});
      setMessage(data.message, 'success');
      retryAfter = data.retryAfter || 60;
    } catch (err) {
      setMessage(err.message, 'error');
      retryAfter = err.retryAfter;
    } finally {
      if (retryAfter > 0) {
        const deadline = Date.now() + retryAfter * 1000;
        const tick = () => {
          if (!button.isConnected) return;
          const left = Math.max(0, Math.ceil((deadline - Date.now()) / 1000));
          button.disabled = left > 0;
          button.textContent = left > 0 ? `${left} 秒后重发` : '获取验证码';
          if (left > 0) setTimeout(tick, 1000);
        };
        tick();
      } else {
        button.disabled = false;
        button.textContent = '获取验证码';
      }
    }
  }

  async function submitLogin(event) {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    try {
      const data = await api('/api/auth/login', {method: 'POST', body: {login: form.get('login'), password: form.get('password')}});
      state.user = data.user;
      state.notice = '';
      sessionChannel?.postMessage('changed');
      drawWidget();
      closeDialog();
      await refreshAccountData();
    } catch (err) {
      setMessage(err.message, 'error');
    }
  }

  async function submitRegister(event) {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    if (!passwordsMatch(form, 'password')) return;
    try {
      const data = await api('/api/auth/register', {method: 'POST', body: {
        username: form.get('username'), email: form.get('email'), password: form.get('password'), confirm_password: form.get('confirm_password'), code: form.get('code')
      }});
      state.user = data.user;
      state.notice = '';
      sessionChannel?.postMessage('changed');
      drawWidget();
      closeDialog();
      await refreshAccountData();
    } catch (err) {
      setMessage(err.message, 'error');
    }
  }

  async function submitReset(event) {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    if (!passwordsMatch(form, 'password')) return;
    try {
      await api('/api/auth/reset-password', {method: 'POST', body: {email: form.get('email'), password: form.get('password'), confirm_password: form.get('confirm_password'), code: form.get('code')}});
      setMessage('密码已重置，可以登录了', 'success');
    } catch (err) {
      setMessage(err.message, 'error');
    }
  }

  function renderProfile(panel) {
    panel.innerHTML = `
      <section class="accountSummary">
        <div><span>身份</span><strong>${levelName(state.user.level)}</strong></div>
        <div class="quotaSummary"><span>剩余额度</span><strong id="quotaRemaining">${fmt(state.user.quotaPoints)}</strong><progress id="quotaProgress" max="1" aria-label="剩余额度比例" aria-describedby="quotaProgressText"></progress><small id="quotaProgressText">正在读取额度...</small></div>
        <div><span>充值积分</span><strong>${fmt(state.user.rechargePoints)}</strong></div>
      </section>
      <section class="accountSection infoList">
        <p><b>用户 ID</b><span>${state.user.id}</span></p>
        <p><b>用户名</b><span>${esc(state.user.username)}</span></p>
        <p><b>邮箱</b><span>${esc(state.user.email)}</span></p>
        <p><b>会员到期</b><span>${state.user.vipExpireTime || '未开通或永久'}</span></p>
      </section>
      <div id="usageRecords" class="usageRecords" aria-label="使用记录"></div>
      <section class="formGrid">
        <form id="passwordForm" class="accountForm"><h3>修改密码</h3><label>原密码<input name="old_password" type="password" autocomplete="current-password" required></label><label>新密码<input name="new_password" type="password" autocomplete="new-password" required minlength="8" maxlength="128"></label><label>确认新密码<input name="confirm_password" type="password" autocomplete="new-password" required minlength="8" maxlength="128"></label><button type="submit">保存密码</button></form>
        <form id="emailForm" class="accountForm"><h3>修改邮箱</h3><label>新邮箱<input name="email" type="email" autocomplete="email" required maxlength="254"></label><div class="inlineFields"><label>验证码<input name="code" inputmode="numeric" autocomplete="one-time-code" required pattern="[0-9]{6}" maxlength="6"></label><button type="button" data-code="change_email">获取验证码</button></div><button type="submit">保存邮箱</button></form>
        <form id="deleteForm" class="accountForm dangerForm"><h3>注销账号</h3><label>当前密码<input name="password" type="password" autocomplete="current-password" required></label><button type="submit">注销账号</button></form>
      </section>
      <button id="logoutButton" class="logoutButton" type="button">退出登录</button>`;
    panel.querySelector('#passwordForm').addEventListener('submit', submitPasswordChange);
    panel.querySelector('#emailForm').addEventListener('submit', submitEmailChange);
    panel.querySelector('#emailForm [data-code]').addEventListener('click', requestCode);
    panel.querySelector('#deleteForm').addEventListener('submit', submitDelete);
    panel.querySelector('#logoutButton').addEventListener('click', logout);
    window.OshUsage.mount(panel.querySelector('#usageRecords'), {api, onQuota(quota) {
      const progress = panel.querySelector('#quotaProgress');
      const text = panel.querySelector('#quotaProgressText');
      if (!progress) return;
      progress.max = quota?.total || 1;
      progress.value = quota?.remaining || 0;
      if (!quota) { text.textContent='额度比例暂不可用'; progress.setAttribute('aria-valuetext',text.textContent); return; }
      panel.querySelector('#quotaRemaining').textContent=fmt(quota.remaining);
      const percent = quota.total > 0 ? Math.min(100, quota.remaining / quota.total * 100) : 0;
      text.textContent=quota.total > 0 ? `${fmt(quota.remaining)} / ${fmt(quota.total)} · 剩余 ${fmt(percent)}%` : '暂无有效额度';
      progress.setAttribute('aria-valuetext',text.textContent);
    }});
  }

  function renderMember(panel) {
    const products = state.paymentProducts || {plans: state.plans, rechargePackages: []};
    const plans = products.plans?.length ? products.plans : state.plans;
    const packages = products.rechargePackages || [];
    const paymentEnabled = products.provider === 'mock' || products.provider === 'huifu';
    const paymentLabel = products.provider === 'mock' ? '创建模拟订单' : products.provider === 'huifu' ? '立即支付' : '支付待配置';
    panel.innerHTML = `
      <section class="memberPlans">${plans.map(plan => `<article class="memberPlan"><strong>${esc(plan.planName)}</strong><b>${money(plan.priceCents)}</b><span>每月 ${fmt(plan.monthlyPoints)} 额度</span><button type="button" data-buy-type="subscription" data-buy-code="${esc(plan.planCode)}" ${paymentEnabled ? '' : 'disabled'}>${paymentLabel}</button></article>`).join('')}</section>
      <section class="accountSection"><div class="sectionTitle"><h3>充值积分</h3><span>长期有效，不随会员周期到期</span></div><div class="rechargePlans">${packages.map(pkg => `<article class="memberPlan"><strong>${esc(pkg.packageName)}</strong><b>${money(pkg.priceCents)}</b><span>到账 ${fmt(pkg.points)} 充值积分</span><button type="button" data-buy-type="recharge" data-buy-code="${esc(pkg.packageCode)}" ${paymentEnabled ? '' : 'disabled'}>${paymentLabel}</button></article>`).join('')}</div></section>
      <section class="accountSection explain">
        <h3>额度和付费逻辑</h3>
        <p>注册赠送 888 充值积分，长期有效，不计入会员额度。订阅 VIP 后，每个自然月发放对应套餐额度：月 VIP 1288、年 VIP 1888、永久 VIP 3888，仅当期有效。</p>
        <p>从月 VIP 升级到年 VIP，或从年 VIP 升级到永久 VIP，当月会补发新旧套餐的额度差额。消费时优先扣即将过期的额度，再扣长期有效的充值积分。</p>
        <p>视频基础价格 5 积分、图片基础价格 1 积分，实际消耗 = 基础价格 × 当前倍率（${fmt(state.pricing?.multiplier ?? 1)}）。当前视频 ${fmt(state.pricing?.video ?? 5)} 积分、图片 ${fmt(state.pricing?.image ?? 1)} 积分，以解锁确认时的价格为准，支持两位小数。</p>
        <p>解锁包含提示词、原始素材和下载。同一账号重复访问不再扣费，倍率变化不影响已购资产。普通用户与 VIP 采用相同价格，会员每月享有套餐额度。</p>
        <p>${products.provider === 'mock' ? '当前为本地模拟支付模式，创建订单后可点击“模拟支付成功”验证完整结算链路；不会产生真实扣款。' : products.provider === 'huifu' ? '支付由汇付统一收银台处理。只有支付平台成功通知并通过金额、商户号和签名校验后，系统才会发放会员或充值积分。' : '支付提供方尚未配置，当前不能创建支付订单。请管理员配置 PAY_PROVIDER 后重启服务。'}</p>
      </section>`;
    panel.querySelectorAll('[data-buy-type]').forEach(button => button.addEventListener('click', () => startPayment(button.dataset.buyType, button.dataset.buyCode, button)));
  }

  function renderOrders(panel) {
    const statusName = status => ({pending:'待支付', success:'已完成', failed:'支付失败', closed:'已关闭'}[status] || status);
    panel.innerHTML = `<section class="accountSection"><div class="sectionTitle"><h3>订阅与充值</h3><span>最近订单</span></div><div class="orderList">${state.orders.length ? state.orders.map(order => `<div class="orderRow"><span>${esc(order.subject)}</span><b>${order.orderType === 'recharge' ? fmt(order.points) + ' 积分' : money(order.amountCents)}</b><small>${statusName(order.status)} · ${esc(order.createdAt)}</small></div>`).join('') : '<div class="empty"><i data-lucide="receipt-text"></i><p>暂无订单记录</p><a class="button" href="/member">前往会员中心</a></div>'}</div></section>`;
  }

  async function startPayment(orderType, code, button) {
    if (button) button.disabled = true;
    try {
      const body = orderType === 'subscription' ? {order_type: orderType, plan_code: code, channel: 'alipay'} : {order_type: orderType, package_code: code, channel: 'alipay'};
      const data = await api('/api/payments/orders', {method: 'POST', body});
      showPaymentDialog(data.order);
    } catch (err) {
      setMessage(err.message, 'error');
    } finally {
      if (button) button.disabled = false;
    }
  }

  function showPaymentDialog(initialOrder) {
    const modal = document.createElement('dialog');
    modal.className = 'paymentDialog';
    document.body.append(modal);
    const statusName = status => ({pending:'待支付', success:'已完成', failed:'支付失败', closed:'已关闭'}[status] || status);
    const render = order => {
      const mock = order.provider === 'mock' && order.status === 'pending';
      modal.innerHTML = `<form method="dialog"><div class="paymentDialogHead"><strong>订单支付</strong><button value="close" aria-label="关闭" title="关闭">×</button></div><div class="paymentDialogBody"><p>${esc(order.subject)}</p><strong class="paymentAmount">¥${esc(order.amount)}</strong><p>订单状态：${statusName(order.status)}</p>${order.payUrl && order.provider !== 'mock' ? `<a class="payLink" href="${esc(order.payUrl)}" target="_blank" rel="noopener">打开支付页面</a>` : ''}${mock ? '<p class="paymentHint">本地模拟支付不会产生真实扣款。</p>' : ''}<div class="paymentActions">${mock ? '<button type="button" data-mock-pay>模拟支付成功</button>' : ''}<button type="button" data-refresh-pay>刷新状态</button>${order.status === 'pending' ? '<button type="button" data-cancel-pay>取消订单</button>' : ''}</div><p class="paymentResult" role="status"></p></div></form>`;
      modal.querySelector('[data-mock-pay]')?.addEventListener('click', async () => {
        try { const data = await api(`/api/payments/${encodeURIComponent(order.orderNo)}/mock-success`, {method:'POST', body:{}}); render(data.order); await refreshAccountData(); }
        catch (err) { modal.querySelector('.paymentResult').textContent = err.message; }
      });
      modal.querySelector('[data-refresh-pay]')?.addEventListener('click', async () => {
        try { const data = await api(`/api/payments/${encodeURIComponent(order.orderNo)}`); render(data.order); }
        catch (err) { modal.querySelector('.paymentResult').textContent = err.message; }
      });
      modal.querySelector('[data-cancel-pay]')?.addEventListener('click', async () => {
        try { const data = await api(`/api/payments/${encodeURIComponent(order.orderNo)}/cancel`, {method:'POST', body:{}}); render(data.order); }
        catch (err) { modal.querySelector('.paymentResult').textContent = err.message; }
      });
    };
    render(initialOrder);
    modal.addEventListener('close', () => modal.remove(), {once:true});
    modal.showModal();
  }

  function renderConsole(panel) {
    if (state.user?.level < 3) { panel.textContent = '仅管理员可访问控制台'; return; }
    panel.innerHTML = `
      <section class="accountSection"><div class="sectionTitle"><h3>资产购买倍率</h3><span>基础价格：视频 5 / 图片 1 积分</span></div>
        <form id="pricingForm" class="pricingForm"><label>倍率<input type="number" name="multiplier" min="0.1" max="1" step="0.01" value="${Number(state.pricing?.multiplier ?? 1)}" required></label><label>倍率范围 0.1–1<input type="range" name="range" min="0.1" max="1" step="0.01" value="${Number(state.pricing?.multiplier ?? 1)}"></label><button type="submit"><i data-lucide="save"></i> 保存倍率</button></form>
        <div class="pricingPreview"><span>视频实际消耗<strong id="videoCost"></strong></span><span>图片实际消耗<strong id="imageCost"></strong></span></div>
      </section>
      <section class="accountSection adminPanel">
        <div class="sectionTitle"><h3>检索用户</h3><span>用户名、邮箱或 ID</span></div>
        <form id="userSearch" class="grantForm"><input name="q" aria-label="用户关键词" placeholder="关键词"><select name="role" aria-label="用户角色"><option value="">全部角色</option><option value="normal">普通</option><option value="vip">VIP</option><option value="admin">管理员</option></select><select name="status" aria-label="用户状态"><option value="">全部状态</option><option value="active">正常</option><option value="blocked">已拉黑</option></select><button type="submit">搜索</button></form>
        <div id="adminUsers" class="adminUsers"></div>
        <div id="adminDetail" class="adminDetail"></div>
      </section>
      <section class="accountSection adminPanel">
        <div class="sectionTitle"><h3>手动升级</h3><span>按用户 ID/用户名/邮箱</span></div>
        <form id="grantMemberForm" class="grantForm"><input name="identity" aria-label="升级用户" placeholder="用户 ID、用户名或邮箱" required><select name="plan_code" aria-label="会员套餐">${state.plans.map(plan => `<option value="${esc(plan.planCode)}">${esc(plan.planName)}</option>`).join('')}</select><button type="submit">开通</button></form>
      </section>`;
    panel.querySelector('#userSearch').addEventListener('submit', submitUserSearch);
    panel.querySelector('#grantMemberForm').addEventListener('submit', submitGrant);
    const pricingForm = panel.querySelector('#pricingForm');
    const preview = () => {
      const hundredths = Math.round(Number(pricingForm.elements.multiplier.value) * 100);
      panel.querySelector('#videoCost').textContent = `${fmt(hundredths * 5 / 100)} 积分`;
      panel.querySelector('#imageCost').textContent = `${fmt(hundredths / 100)} 积分`;
    };
    pricingForm.elements.multiplier.addEventListener('input', () => { pricingForm.elements.range.value = pricingForm.elements.multiplier.value; preview(); });
    pricingForm.elements.range.addEventListener('input', () => { pricingForm.elements.multiplier.value = pricingForm.elements.range.value; preview(); });
    pricingForm.addEventListener('submit', async event => {
      event.preventDefault(); const button = pricingForm.querySelector('button'); button.disabled = true;
      try {
        state.pricing = await api('/api/admin/pricing', {method:'POST', body:{multiplier:Number(pricingForm.elements.multiplier.value),version:state.pricing?.version}});
        setMessage('倍率已保存，新的购买按此价格结算。', 'success');
      } catch (err) {
        setMessage(err.message, 'error');
        if(err.status===409) {
          try { state.pricing=await api('/api/admin/pricing'); renderConsole(panel); }
          catch { setMessage('价格刷新失败，请重新加载页面后重试','error'); }
        }
      } finally { button.disabled = false; }
    });
    preview();
    renderAdminUsers(panel.querySelector('#adminUsers'));
    window.OshShell?.icons();
  }

  function renderAdminUsers(box) {
    if (!box) return;
    if (!state.adminUsers.length) {
      box.textContent = '暂无用户结果';
      return;
    }
    box.innerHTML = state.adminUsers.map(user => `<article class="adminUser"><strong>${esc(user.username)}</strong><span>${esc(user.email)} · ${levelName(user.level)} · ${user.status === 1 ? '已拉黑' : '正常'}</span><div><button type="button" data-detail="${user.id}">详情</button>${user.status === 1 ? `<button type="button" data-unblock="${user.id}">取消拉黑</button>` : `<button type="button" data-block="${user.id}">拉黑</button>`}</div></article>`).join('');
    for (const button of box.querySelectorAll('[data-block]')) button.addEventListener('click', () => blockUser(Number(button.dataset.block), true));
    for (const button of box.querySelectorAll('[data-unblock]')) button.addEventListener('click', () => blockUser(Number(button.dataset.unblock), false));
    for (const button of box.querySelectorAll('[data-detail]')) button.addEventListener('click', () => loadUserDetail(Number(button.dataset.detail)));
  }

  function passwordsMatch(form, field) {
    if (!form.get('confirm_password') || form.get(field) !== form.get('confirm_password')) {
      setMessage('两次输入的密码不一致', 'error');
      return false;
    }
    return true;
  }

  async function submitPasswordChange(event) {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    if (!passwordsMatch(form, 'new_password')) return;
    try {
      await api('/api/user/password', {method: 'POST', body: {old_password: form.get('old_password'), new_password: form.get('new_password'), confirm_password: form.get('confirm_password')}});
      setMessage('密码已修改', 'success');
    } catch (err) { setMessage(err.message, 'error'); }
  }

  async function submitEmailChange(event) {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    try {
      await api('/api/user/email', {method: 'POST', body: {email: form.get('email'), code: form.get('code')}});
      state.user.email = form.get('email');
      setMessage('邮箱已修改', 'success');
      drawWidget();
    } catch (err) { setMessage(err.message, 'error'); }
  }

  async function submitDelete(event) {
    event.preventDefault();
    if (!confirm('确认注销当前账号？注销后将无法登录或使用该账号的权益。')) return;
    const form = new FormData(event.currentTarget);
    try {
      await api('/api/user/delete', {method: 'POST', body: {password: form.get('password')}});
      state.user = null;
      state.records = [];
      state.orders = [];
      state.adminUsers = [];
      state.notice = '';
      sessionChannel?.postMessage('changed');
      drawWidget();
      closeDialog();
      renderWorkspace();
    } catch (err) { setMessage(err.message, 'error'); }
  }

  async function logout() {
    if (!confirm('确认退出登录？')) return;
    try {
      await api('/api/auth/logout', {method: 'POST'});
    } catch (err) {
      setMessage(err.message, 'error');
      return;
    }
    state.user = null;
    sessionChannel?.postMessage('changed');
    state.records = [];
    state.orders = [];
    state.adminUsers = [];
    state.notice = '';
    drawWidget();
    closeDialog();
    renderWorkspace();
  }

  async function submitUserSearch(event) {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    const params = new URLSearchParams({q: form.get('q') || '', role: form.get('role') || '', status: form.get('status') || ''});
    try {
      const data = await api(`/api/admin/users?${params}`);
      state.adminUsers = data.users || [];
      renderAdminUsers(document.querySelector('#adminUsers'));
    } catch (err) { setMessage(err.message, 'error'); }
  }

  async function submitGrant(event) {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    const identity = String(form.get('identity') || '').trim();
    const body = {plan_code: form.get('plan_code')};
    if (/^\d+$/.test(identity)) body.user_id = Number(identity);
    else if (identity.includes('@')) body.email = identity;
    else body.username = identity;
    try {
      await api('/api/admin/member/grant', {method: 'POST', body});
      setMessage('会员已开通或升级', 'success');
    } catch (err) { setMessage(err.message, 'error'); }
  }

  async function blockUser(userID, blocked) {
    try {
      await api(blocked ? '/api/admin/users/block' : '/api/admin/users/unblock', {method: 'POST', body: {user_id: userID, reason: '管理员操作'}});
      state.adminUsers = state.adminUsers.map(user => user.id === userID ? {...user, status:blocked ? 1 : 0} : user);
      renderAdminUsers(document.querySelector('#adminUsers'));
      setMessage(blocked ? '已拉黑用户' : '已取消拉黑', 'success');
    } catch (err) { setMessage(err.message, 'error'); }
  }

  async function loadUserDetail(userID) {
    try {
      const data = await api(`/api/admin/users/${userID}`);
      const box = document.querySelector('#adminDetail');
      if (box) {
        box.innerHTML = `<h3>${esc(data.user.username)}</h3>
          <p>ID ${data.user.id} · ${esc(data.user.email)} · ${levelName(data.user.level)} · ${data.user.status === 1 ? '已拉黑' : '正常'}</p>
          <p>剩余额度 ${fmt(data.user.quotaPoints)} · 充值积分 ${fmt(data.user.rechargePoints)} · 总积分 ${fmt(data.user.totalPoints)}</p>
          <strong>最近订单</strong>
          <div>${data.orders.length ? data.orders.slice(0, 5).map(order => `<span>${esc(order.subject)} / ${esc(order.orderType)} / ${esc(order.createdAt)}</span>`).join('') : '暂无订单'}</div>`;
      }
    } catch (err) { setMessage(err.message, 'error'); }
  }

  async function refreshAccountData() {
    const tasks = [api('/api/member/plans'), api('/api/payments/products'), api('/api/user/profile'), api('/api/points/records'), api('/api/orders')];
    const [plans, products, profile, records, orders] = await Promise.all(tasks);
    state.plans = plans.plans || [];
    state.paymentProducts = products;
    state.user = {...state.user, ...profile.user, ...profile.balance};
    state.records = records.records || [];
    state.orders = orders.orders || [];
    drawWidget();
    renderWorkspace();
  }

  async function init() {
    const header = document.querySelector('header');
    if (!header) return;
    widget = document.createElement('div');
    widget.className = 'accountWidget';
    (header.querySelector('#accountSlot') || header).append(widget);
    dialog = document.createElement('dialog');
    dialog.className = 'accountDialog';
    document.body.append(dialog);
    dialog.addEventListener('close', () => { message=document.getElementById('workspaceMessage'); });
    dialog.addEventListener('click', event => { if (event.target === dialog) closeDialog(); });
    try {
      const me = await api('/api/me');
      state.user = me.authenticated ? me.user : null;
    } catch { state.user = null; }
    try {
      const plans = await api('/api/member/plans');
      state.plans = plans.plans || [];
    } catch { state.plans = []; }
    state.paymentProducts = await api('/api/payments/products').catch(() => ({provider:'disabled', plans:state.plans, rechargePackages:[]}));
    state.pricing = await api('/api/pricing').catch(() => null);
    drawWidget();
    if (workspace && state.user) {
      try { await refreshAccountData(); } catch(err) { renderWorkspace(); setMessage(err.message,'error'); }
    } else renderWorkspace();
  }

  async function refreshUser() {
    const me = await api('/api/me');
    state.user = me.authenticated ? me.user : null;
    drawWidget();
  }

  function offerAccount(text) {
    state.notice = text;
    if (state.user) { location.href = '/member'; return; }
    openDialog();
  }

  function confirmUnlock(kind, cost, balance) {
    return new Promise(resolve => {
      const modal = document.createElement('dialog');
      modal.className = 'resourceDialog';
      modal.innerHTML = `<form method="dialog"><h3>解锁${kind === 'video' ? '视频' : '图片'}资源</h3><p>本次扣除 <strong>${cost} 积分</strong>，当前可用 ${fmt(balance)} 积分。</p><p>包含完整提示词、${kind === 'video' ? '视频与参考素材' : '原图'}及下载，重复访问不再扣费。</p><div><button value="cancel">取消</button><button value="unlock" autofocus>确认解锁</button></div></form>`;
      document.body.append(modal);
      modal.addEventListener('close', () => { const ok = modal.returnValue === 'unlock'; modal.remove(); resolve(ok); }, {once: true});
      modal.showModal();
    });
  }

  async function ensureResource(kind, id) {
    if (unlockInProgress) return false;
    unlockInProgress = true;
    try {
      const path = `/api/resources/${kind}/${encodeURIComponent(id)}`;
      const access = await api(`${path}/access`);
      if (!access.authenticated) {
        await refreshUser();
        offerAccount('请先登录后使用积分解锁。积分不足时可充值或订阅会员。');
        return false;
      }
      if (access.unlocked) return true;
      if (access.balance < access.cost) {
        await refreshUser();
        offerAccount(`积分不足，解锁需要 ${access.cost} 积分，请充值或订阅会员。`);
        return false;
      }
      if (!await confirmUnlock(kind, access.cost, access.balance)) return false;
      await api(`${path}/unlock`, {method: 'POST', body: {price_version:access.price_version}});
      await refreshUser();
      return true;
    } catch (err) {
      if (err.status === 409) {
        const pricing = await api('/api/pricing').catch(() => null);
        if (pricing) window.dispatchEvent(new CustomEvent('osh-price-change', {detail:pricing}));
      }
      if (err.status === 401 || err.status === 402) {
        await refreshUser().catch(() => {});
        offerAccount(err.message);
        return false;
      }
      throw err;
    } finally { unlockInProgress = false; }
  }

  function renderWorkspace() {
    if (!workspace) return;
    window.OshUsage?.destroy();
    if (!dialog.open) message = document.getElementById('workspaceMessage');
    const path = location.pathname;
    const view = new URLSearchParams(location.search).get('view') || 'profile';
    const titles = {profile:'基本信息',orders:'订单记录',console:'控制台'};
    const title = path === '/assets' ? '我的资产' : path === '/member' ? '会员中心' : titles[view] || '基本信息';
    document.getElementById('workspaceTitle').textContent = title;
    document.title = `${title} · OSH 灵感库`;
    if (path === '/member') { renderMember(workspace); return; }
    if (!state.user) {
      assetRequest++;
      workspace.innerHTML = '<div class="empty"><i data-lucide="lock-keyhole"></i><p>登录后查看你的空间</p><button type="button" id="workspaceLogin">登录 / 注册</button></div>';
      workspace.querySelector('button').addEventListener('click',openDialog);
      window.OshShell?.icons();
      if (!dialog.open) openDialog();
      return;
    }
    if (path === '/assets') { renderAssets(); return; }
    if (view === 'console') {
      if (state.user.level < 3) { workspace.textContent='仅管理员可访问控制台'; return; }
      renderConsole(workspace);
    } else if (view === 'orders') renderOrders(workspace);
    else renderProfile(workspace);
    window.OshShell?.icons();
  }

  async function renderAssets() {
    const request = ++assetRequest;
    workspace.innerHTML = `<div class="assetFilters" role="group" aria-label="资产类型">${[['all','全部资产'],['video','视频提示词'],['image','图片提示词']].map(([kind,label])=>`<button type="button" data-kind="${kind}" aria-pressed="${kind===state.assetKind}" class="${kind===state.assetKind?'selected':''}">${label}</button>`).join('')}</div><div class="resultline"><strong id="assetTotal">正在加载...</strong></div><section class="grid" id="assetGrid"></section><nav class="pagination" id="assetPagination" aria-label="资产翻页"></nav>`;
    workspace.querySelectorAll('[data-kind]').forEach(button=>button.addEventListener('click',()=>{state.assetKind=button.dataset.kind;state.assetPage=1;renderAssets();}));
    try {
      const data = await api(`/api/assets?kind=${state.assetKind}&page=${state.assetPage}`);
      if (request !== assetRequest || !state.user) return;
      document.getElementById('assetTotal').textContent = `${fmt(data.total)} 项已购资产`;
      document.getElementById('assetGrid').innerHTML = data.items.length ? data.items.map(item => `<article class="card">${item.thumbnail?`<img class="purchasedImage" src="${esc(item.thumbnail)}" alt="${esc(item.title)}" loading="lazy">`:'<div class="thumb">暂无预览</div>'}<div class="cardBody"><h2>${esc(item.title)}</h2><div class="assetMeta"><span>${item.kind==='video'?'视频':'图片'} · ${esc(item.model)}</span><span>实付 <b>${fmt(item.paidPoints)} 积分</b></span><span>${esc(item.purchasedAt)}</span></div>${item.available?`<a class="promptAction" href="${item.kind==='video'?'/':'/images.html'}?resource=${encodeURIComponent(item.id)}"><i data-lucide="file-text"></i>查看已购提示词</a>`:'<button disabled>资源暂不可用</button>'}</div></article>`).join('') : '<div class="empty"><i data-lucide="folder-open"></i><p>暂无已购资产</p><a class="button" href="/">探索视频</a> <a class="button" href="/images.html">探索图片</a></div>';
      const pages = Math.max(1,Math.ceil(data.total/data.size));
      const pagination = document.getElementById('assetPagination');
      pagination.innerHTML = `<button type="button" data-page="-1" ${data.page<=1?'disabled':''}>上一页</button><span>第 ${data.page} / ${pages} 页</span><button type="button" data-page="1" ${data.page>=pages?'disabled':''}>下一页</button>`;
      pagination.querySelectorAll('button').forEach(button=>button.addEventListener('click',()=>{state.assetPage+=Number(button.dataset.page);renderAssets();}));
      window.OshShell?.icons();
    } catch(err) { if(request===assetRequest) { setMessage(err.message,'error'); document.getElementById('assetTotal').textContent='加载失败'; } }
  }

  window.OshAccount = {ensureResource, api, refreshUser, ready:init()};
  if (sessionChannel) sessionChannel.onmessage = () => {
    window.dispatchEvent(new CustomEvent('osh-user-change'));
    refreshUser().then(() => state.user ? refreshAccountData() : renderWorkspace()).catch(() => {});
  };

})();
