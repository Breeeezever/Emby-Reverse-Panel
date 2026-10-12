// Panel endpoint validation, certificate setup and restart workflow.

function currentPanelAccessPort(location = typeof window !== 'undefined' ? window.location : null) {
  if (!location || !['http:', 'https:'].includes(location.protocol)) return null;
  return parseBoundedInteger(location.port || (location.protocol === 'https:' ? '443' : '80'), 1, 65535);
}

function panelEndpointFormPort(status, location) {
  const saved = parseBoundedInteger(status.listen_port, 1, 65535);
  // This input changes the server listener, never the external proxy's port.
  return saved || parseBoundedInteger(status.active_listen_port, 1, 65535) || currentPanelAccessPort(location) || 9090;
}

function panelEndpointValidationHTML(validation) {
  const title = !validation.can_save ? '此配置不能保存'
    : !validation.can_restart ? '可保存待应用设置，重启条件尚未满足'
    : '配置检查通过';
  const checks = Array.isArray(validation.checks) ? validation.checks : [];
  return `<strong>${esc(title)}</strong><ul>${checks.map(check => {
    const state = ['ok', 'warning', 'blocked'].includes(check.state) ? check.state : 'warning';
    const label = state === 'ok' ? '通过' : state === 'blocked' && !check.restart_only ? '阻止' : '待处理';
    return `<li class="panel-endpoint-check ${state}${check.restart_only ? ' restart-only' : ''}"><span>${label}</span><div>${esc(check.message || '')}</div></li>`;
  }).join('')}</ul>`;
}

async function renderTLSSettings() {
  const page = document.getElementById('page-settings-tls');
  if (!page) return;
  page.innerHTML = `<div class="settings-layout fade-up">${globalSettingsNav('tls')}<main class="settings-content"><div class="settings-loading">正在读取 TLS 配置…</div></main></div>`;
  bindGlobalSettingsNav(page);
  try {
    const status = await API.panelCertificate();
    if (Router.current !== 'settings-tls') return;
    const savedTLS = status.saved_tls_enabled === true;
    const activeURL = status.external_proxy ? window.location.origin : status.active_access_url || window.location.origin;
    const portHelp = status.external_proxy
      ? '这是反向代理的内部回源端口，不能只在面板修改。需要切换时请使用安装器或同步调整部署配置。'
      : '保存后需应用并重启。新端口还需在云防火墙或容器映射中放行。';
    page.innerHTML = `<div class="settings-layout fade-up">${globalSettingsNav('tls')}<main class="settings-content">
      <section class="settings-panel" id="p-endpoint-settings">
        <header><h2>面板访问地址</h2></header>
        <div class="panel-endpoint-current"><span>当前生效地址</span><strong class="mono">${esc(activeURL)}</strong><small>${status.external_proxy ? '外部反向代理管理入口' : status.tls_enabled ? 'Meridian 原生 HTTPS' : 'Meridian HTTP'}</small></div>
        <p class="form-help" id="p-endpoint-state"></p>
        <div class="global-settings-status">${renderPanelCertificateStatus(status)}</div>
        <div class="settings-grid" style="margin-top:20px">
          <label class="settings-field"><span>面板域名</span><input class="form-input" id="p-panel-domain" value="${esc(status.panel_domain || '')}" placeholder="panel.example.com" required autocomplete="off" autocapitalize="none" spellcheck="false"><small>不含协议和端口。</small></label>
          <label class="settings-field"><span>节点泛域名（可选）</span><input class="form-input" id="p-wildcard-domain" value="${esc(status.wildcard_domain || '')}" placeholder="*.example.com" autocomplete="off" autocapitalize="none" spellcheck="false"><small>使用域名前缀入口或申请泛域名证书时填写。</small></label>
          <label class="settings-field"><span>面板监听端口</span><input class="form-input" id="p-panel-listen-port" type="number" min="1" max="65535" step="1" inputmode="numeric" required value="${esc(String(panelEndpointFormPort(status)))}"><small>${esc(portHelp)}</small></label>
          <div class="settings-field"><span>应用后的访问地址</span><div class="form-help mono" id="p-panel-address-preview"></div></div>
        </div>
        <div class="settings-save-bar tls-settings-actions"><button class="telegram-btn" id="p-endpoint-check" type="button">检查配置</button><button class="telegram-btn primary" id="p-cert-save" type="button">检查并保存</button></div>
        <div class="panel-endpoint-validation" id="p-endpoint-validation" role="status" aria-live="polite"></div>
        <p class="form-help">检查解析、代理端口、监听、站点冲突与证书条件；检查通过不代表云防火墙或外部转发已放行。</p>
        <div class="settings-save-bar" ${status.restart_required ? '' : 'hidden'}><button class="telegram-btn primary" id="p-cert-restart" type="button" disabled>应用设置并重启</button></div>
      </section>
      <section class="settings-panel">
        <header><h2>配置证书</h2></header>
        <label class="settings-field"><span>证书来源</span><select class="form-select" id="p-cert-source"><option value="acme">自动申请（Cloudflare DNS）</option><option value="path">服务器文件路径</option><option value="upload">上传证书与私钥</option></select></label>
        <div id="p-source-acme" class="tls-source-fields">
          <p class="settings-panel-help">通过 DNS 验证申请泛域名证书，签发后自动续期，无需开放 80 端口。请先保存节点泛域名。</p>
          <label class="settings-field"><span>ACME 邮箱</span><input class="form-input" id="p-acme-email" type="email" value="${esc(status.acme_email || '')}" autocomplete="email" placeholder="admin@example.com"></label>
          <label class="settings-field"><span>Cloudflare DNS API Token</span><input class="form-input" id="p-acme-token" type="password" autocomplete="new-password" maxlength="512" placeholder="${status.dns_api_token_configured ? '已保存，留空继续使用' : '填写 DNS API Token'}"><small>需要对应域名的 Zone:Read 与 DNS:Edit 权限。</small></label>
          <label class="settings-check"><input id="p-acme-staging" type="checkbox" ${status.acme_staging ? 'checked' : ''}><span>使用 ACME 测试环境</span></label>
        </div>
        <div id="p-source-path" class="tls-source-fields" hidden>
          <p class="settings-panel-help">读取服务器上的 PEM 文件；外部续期后会自动重新加载。Meridian 服务账户需要文件读取权限。</p>
          <label class="settings-field"><span>完整证书链路径</span><input class="form-input" id="p-certificate-path" value="${esc(status.certificate_path || '')}" placeholder="/etc/letsencrypt/live/example.com/fullchain.pem" autocomplete="off" spellcheck="false"></label>
          <label class="settings-field"><span>私钥路径</span><input class="form-input" id="p-private-key-path" value="${esc(status.private_key_path || '')}" placeholder="/etc/letsencrypt/live/example.com/privkey.pem" autocomplete="off" spellcheck="false"></label>
        </div>
        <div id="p-source-upload" class="tls-source-fields" hidden>
          <p class="settings-panel-help">上传 PEM 格式的完整证书链和未加密私钥。更新证书时再次上传即可。</p>
          <label class="settings-field"><span>完整证书链文件</span><input class="form-input" id="p-certificate-file" type="file" accept=".pem,.crt,.cer"></label>
          <label class="settings-field"><span>私钥文件</span><input class="form-input" id="p-private-key-file" type="file" accept=".pem,.key"></label>
        </div>
        <div class="settings-save-bar tls-settings-actions">
          <button class="telegram-btn primary" id="p-cert-apply" type="button" ${status.issuing ? 'disabled' : ''}>应用证书</button>
        </div>
        <p class="form-help" id="p-cert-result" role="status"></p>
      </section>
    </main></div>`;
    bindGlobalSettingsNav(page);
    const get = id => page.querySelector(`#p-${id}`);
    const validationElement = get('endpoint-validation');
    const stillCurrent = () => Router.current === 'settings-tls' && page.querySelector('#p-endpoint-validation') === validationElement;
    const source = get('cert-source');
    let busy = false, checkSequence = 0, validation = null;
    const savedPayload = {panel_domain: String(status.panel_domain || ''), wildcard_domain: String(status.wildcard_domain || ''), listen_port: Number(status.listen_port)};
    source.value = ['acme', 'path', 'upload'].includes(status.certificate_source) ? status.certificate_source : 'acme';
    const syncSource = () => {
      for (const name of ['acme', 'path', 'upload']) get(`source-${name}`).hidden = source.value !== name;
      get('cert-apply').textContent = source.value === 'acme' ? '申请并应用证书' : source.value === 'path' ? '读取并应用证书' : '上传并应用证书';
    };
    source.onchange = syncSource; syncSource();
    const settingsPayload = () => ({panel_domain: get('panel-domain').value.trim(), wildcard_domain: get('wildcard-domain').value.trim(), listen_port: Number(get('panel-listen-port').value)});
    const sameSettings = (left, right) => left.panel_domain.toLowerCase() === right.panel_domain.toLowerCase() && left.wildcard_domain.toLowerCase() === right.wildcard_domain.toLowerCase() && left.listen_port === right.listen_port;
    const dirty = () => !sameSettings(settingsPayload(), savedPayload);
    const syncActions = () => {
      const unsaved = dirty();
      get('endpoint-state').textContent = unsaved ? '下方修改尚未保存，当前生效地址保持不变。'
        : status.restart_required ? '已有保存的设置等待应用；下方表单尚未替换当前生效地址。'
        : '下方为已保存设置。修改后先检查并保存，再应用并重启。';
      get('endpoint-state').classList.toggle('panel-endpoint-pending', unsaved || status.restart_required);
      get('cert-save').disabled = busy || Boolean(validation && !validation.can_save);
      get('endpoint-check').disabled = busy;
      get('cert-apply').disabled = busy || dirty() || status.issuing || status.external_proxy || !validation || !validation.can_save;
      get('cert-restart').disabled = busy || dirty() || !validation || !validation.can_restart || !status.restart_required;
      for (const id of ['panel-domain', 'wildcard-domain', 'panel-listen-port']) get(id).disabled = busy;
      get('cert-save').textContent = busy ? '处理中…' : '检查并保存';
    };
    const showValidation = result => {
      validation = result;
      get('endpoint-validation').innerHTML = panelEndpointValidationHTML(result);
      syncActions();
    };
    const showError = error => {
      if (error.validation) showValidation(error.validation);
      else { validation = null; get('endpoint-validation').textContent = error.message; syncActions(); }
    };
    const checkSettings = async () => {
      const sequence = ++checkSequence, payload = settingsPayload();
      validation = null;
      get('endpoint-validation').textContent = '正在检查配置…';
      syncActions();
      try {
        const result = await API.checkPanelSettings(payload);
        if (stillCurrent() && sequence === checkSequence && sameSettings(payload, settingsPayload())) showValidation(result);
      } catch (error) {
        if (stillCurrent() && sequence === checkSequence) showError(error);
      }
    };
    const refreshPreview = () => {
      const port = parseBoundedInteger(get('panel-listen-port').value, 1, 65535);
      const domain = get('panel-domain').value.trim();
      const protocol = status.external_proxy ? window.location.protocol : savedTLS ? 'https:' : 'http:';
      const publicPort = status.external_proxy ? currentPanelAccessPort() : port;
      const defaultPort = protocol === 'https:' ? 443 : 80;
      get('panel-address-preview').textContent = port === null ? '请填写有效端口（1–65535）' : domain ? `${protocol}//${domain}${publicPort === defaultPort ? '' : `:${publicPort}`}` : '—';
    };
    for (const id of ['panel-domain', 'wildcard-domain', 'panel-listen-port']) get(id).addEventListener('input', () => {
      checkSequence++; validation = null;
      get('endpoint-validation').textContent = '内容已修改，请重新检查并保存。';
      refreshPreview(); syncActions();
    });
    refreshPreview();
    get('endpoint-check').onclick = () => { if (validateFields(get('endpoint-settings'))) checkSettings(); };
    get('cert-save').onclick = async () => {
      const button = get('cert-save');
      if (button.disabled || !validateFields(get('endpoint-settings'))) return;
      checkSequence++; busy = true; syncActions();
      get('endpoint-validation').textContent = '正在检查并保存…';
      try { await API.savePanelSettings(settingsPayload()); window.location.reload(); }
      catch (error) { if (button.isConnected) { busy = false; showError(error); } }
    };
    get('cert-apply').onclick = async () => {
      const button = get('cert-apply'); if (button.disabled) return;
      const current = settingsPayload();
      if (current.panel_domain.toLowerCase() !== String(status.panel_domain || '').toLowerCase() || current.wildcard_domain.toLowerCase() !== String(status.wildcard_domain || '').toLowerCase() || current.listen_port !== Number(status.listen_port)) return Toast.error('请先保存域名与端口');
      let payload;
      if (source.value === 'acme') {
        if (!current.wildcard_domain) return Toast.error('申请泛域名证书前，请填写并保存节点泛域名');
        if (!get('acme-email').value.trim() || !get('acme-email').checkValidity()) return rejectField(get('acme-email'), '请填写有效邮箱');
        if (!get('acme-token').value.trim() && !status.dns_api_token_configured) return rejectField(get('acme-token'), '请填写 DNS API Token');
        payload = {email:get('acme-email').value.trim(),dns_provider:'cloudflare',dns_api_token:get('acme-token').value.trim(),staging:get('acme-staging').checked};
      } else if (source.value === 'path') {
        if (!get('certificate-path').value.trim() || !get('private-key-path').value.trim()) return Toast.error('请填写证书与私钥路径');
        payload = {source:'path',certificate_path:get('certificate-path').value.trim(),private_key_path:get('private-key-path').value.trim()};
      } else {
        const cert = get('certificate-file').files[0], key = get('private-key-file').files[0];
        if (!cert || !key) return Toast.error('请选择证书链和私钥文件');
        if (cert.size > 512*1024 || key.size > 64*1024) return Toast.error('证书不能超过 512 KB，私钥不能超过 64 KB');
        payload = {source:'upload', certFile:cert, keyFile:key};
      }
      checkSequence++; busy = true; syncActions();
      source.disabled = true; button.textContent = source.value === 'acme' ? '签发中，请稍候…' : '校验并应用中…';
      get('cert-result').textContent = source.value === 'acme' ? '正在申请证书，DNS 验证最多等待 2 分钟，签发流程设有 5 分钟超时，请勿重复提交。' : '正在校验并应用证书…';
      try {
        if (payload.source === 'upload') payload = {source:'upload',certificate_pem:await payload.certFile.text(),private_key_pem:await payload.keyFile.text()};
        const updated = source.value === 'acme' ? await API.requestPanelCertificate(payload) : await API.importPanelCertificate(payload);
        Toast.success(updated.restart_required ? '证书已保存，请重启以启用 HTTPS' : '证书已应用');
        if (updated.restart_required) window.location.reload();
        else await renderTLSSettings();
      } catch (error) { if (button.isConnected) {busy=false;showError(error);get('cert-result').textContent='证书操作失败：'+error.message;source.disabled=false;syncSource();syncActions();} }
    };
    const restart = get('cert-restart');
    if (restart) restart.onclick = async () => {
      if (!window.confirm('重启会短暂中断面板与站点连接，确定继续吗？')) return;
      busy = true; checkSequence++; syncActions(); restart.textContent = '重启中…';
      try { const result = await API.restartSystem(); await waitForPanelRestart(result.redirect_url); }
      catch (error) { if(restart.isConnected){busy=false;showError(error);restart.textContent='应用设置并重启';} }
    };
    if (status.external_proxy) get('cert-result').textContent = '当前入口由外部反向代理提供 HTTPS，请在代理中管理证书。';
    syncActions();
    await checkSettings();
  } catch (error) {
    if (Router.current !== 'settings-tls') return;
    page.querySelector('.settings-content').innerHTML = `<div class="settings-loading request-log-error">读取 TLS 状态失败：${esc(error.message)}</div>`;
  }
}

function renderPanelCertificateStatus(status) {
	if (!status || status.available === false) {
		return '<div class="form-help" style="color:var(--orange)">TLS 数据目录不可写，请检查数据库所在数据目录的权限。</div>';
	}
	if (!status.configured) {
		return '<div class="form-help">尚未配置证书</div>';
	}
	return `
	  <div class="diag-row"><span class="diag-key">证书域名</span><span class="diag-val">${esc(status.subject || `*.${status.route_domain || ''}`)}</span></div>
	  <div class="diag-row"><span class="diag-key">证书匹配</span><span class="diag-val ${status.certificate_current ? 'good' : 'warn'}">${status.certificate_current ? '域名匹配' : '需要申请或更新'}</span></div>
	  <div class="diag-row"><span class="diag-key">到期时间</span><span class="diag-val">${esc(status.expires_at || '—')}</span></div>
	  <div class="diag-row"><span class="diag-key">证书状态</span><span class="diag-val ${status.certificate_valid ? 'good' : 'warn'}">${status.certificate_valid ? '有效' : '已过期或不可用'}</span></div>
	  <div class="diag-row"><span class="diag-key">自动续签</span><span class="diag-val ${status.auto_renew_enabled ? 'good' : 'warn'}">${status.certificate_source === 'path' ? '跟随服务器文件更新' : status.certificate_source === 'upload' ? '到期前重新上传' : status.auto_renew_enabled ? '已启用（到期前 30 天）' : '待配置邮箱和 Token'}</span></div>
	  <div class="diag-row"><span class="diag-key">当前监听端口</span><span class="diag-val">${esc(String(status.active_listen_port || '—'))}</span></div>
	  <div class="diag-row"><span class="diag-key">设置监听端口</span><span class="diag-val ${status.listen_port !== status.active_listen_port ? 'warn' : ''}">${esc(String(status.listen_port || '—'))}</span></div>
	  <div class="diag-row"><span class="diag-key">面板 HTTPS</span><span class="diag-val ${status.restart_required ? 'warn' : 'good'}">${status.restart_required ? '等待重启应用' : status.tls_enabled ? '已启用' : '未启用'}</span></div>
	`;
}

async function waitForPanelRestart(redirectURL) {
	const target = new URL(redirectURL, window.location.href);
	Toast.info('服务正在重启，等待新的面板地址就绪…');
	const deadline = Date.now() + 90000;
	while (Date.now() < deadline) {
		try {
			const response = await fetch(new URL('/api/panel-ready', target).href, {
				mode: 'cors', credentials: 'omit', cache: 'no-store', redirect: 'error',
				signal: AbortSignal.timeout(5000),
			});
			if (response.ok && (await response.json()).ready === true) {
				window.location.replace(target.href);
				return;
			}
		} catch (_) {}
		await new Promise(resolve => setTimeout(resolve, 1500));
	}
	Toast.error(`服务重启超时，请检查域名解析与端口，然后打开 ${target.origin}`);
}
