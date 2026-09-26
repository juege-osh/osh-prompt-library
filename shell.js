(() => {
  const icon = name => `<i data-lucide="${name}" aria-hidden="true"></i>`;
  const path = location.pathname;
  const view = new URLSearchParams(location.search).get('view') || 'profile';
  const active = key => key === (path === '/account' ? view : path) ? ' active' : '';
  const link = (href, key, name, glyph, extra = '') => `<a href="${href}" class="navLink${active(key)}" ${extra}>${icon(glyph)}<span>${name}</span></a>`;
  const sidebar = document.createElement('aside');
  sidebar.className = 'sidebar'; sidebar.id = 'sidebar';
  sidebar.innerHTML = `<a class="brand" href="/"><span class="brandMark">${icon('scan-line')}</span><span><strong>OSH 灵感库</strong><small>PROMPT LIBRARY</small></span></a>
    <nav aria-label="主导航">
      <details class="navGroup" open><summary>${icon('sparkles')}<span>创作</span>${icon('chevron-down')}</summary><div class="navChildren">${link('/', '/', '视频', 'clapperboard')}${link('/images.html', '/images.html', '图片', 'image')}</div></details>
      ${link('/assets', '/assets', '资产', 'folder-heart')}
      <details class="navGroup" open><summary>${icon('user-round')}<span>用户中心</span>${icon('chevron-down')}</summary><div class="navChildren">${link('/account', 'profile', '基本信息', 'contact-round')}${link('/account?view=orders', 'orders', '订单记录', 'receipt-text')}${link('/account?view=console', 'console', '控制台', 'sliders-horizontal', 'data-admin-only hidden')}</div></details>
      ${link('/member', '/member', '会员中心', 'crown')}
    </nav><div class="sidebarFooter"><a href="/member">${icon('crown')}<span>VIP 会员</span>${icon('arrow-up-right')}</a><small>每月创作额度</small></div>`;
  document.body.prepend(sidebar);
  const shade = document.createElement('button');
  shade.className = 'navBackdrop'; shade.type = 'button'; shade.setAttribute('aria-label','关闭导航'); shade.hidden = true;
  document.body.append(shade);
  const header = document.querySelector('header');
  header.className = 'topbar';
  header.innerHTML = `<div class="topbarLeft"><button type="button" class="iconButton menuToggle" title="打开导航" aria-label="打开导航" aria-controls="sidebar" aria-expanded="false">${icon('menu')}</button><span>灵感，从这里开始</span></div><div class="topbarRight"><a class="memberLink" href="/member">${icon('crown')}<span>会员中心</span></a><div id="accountSlot"></div></div>`;
  const toggle = header.querySelector('.menuToggle');
  function setOpen(open) { sidebar.classList.toggle('open', open); shade.hidden = !open; toggle.setAttribute('aria-expanded', String(open)); document.body.classList.toggle('navOpen',open); if(open) sidebar.querySelector('a').focus(); else toggle.focus(); }
  toggle.addEventListener('click', () => setOpen(!sidebar.classList.contains('open')));
  shade.addEventListener('click', () => setOpen(false));
  document.addEventListener('keydown', event => { if(event.key==='Escape' && sidebar.classList.contains('open')) setOpen(false); });
  window.OshShell = {
    icons: () => window.lucide?.createIcons(),
    updateUser(user) { document.querySelectorAll('[data-admin-only]').forEach(node => node.hidden = !(user?.level >= 3)); }
  };
  for (const anchor of sidebar.querySelectorAll('.active')) anchor.setAttribute('aria-current','page');
  window.OshShell.icons();
})();
