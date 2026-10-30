import { formatNumber, message, getIntlLocale, t as tr } from "./i18n/index.js";
import { LanguageSwitcher } from './i18n/LocaleProvider.jsx';
import { useTranslation } from 'react-i18next';
import { Trans } from "react-i18next";
import { useEffect, useRef, useState } from 'react';
import LandingPage from './LandingPage.jsx';
import ModelPlaza from './ModelPlaza.jsx';
import DownloadPage from './DownloadPage.jsx';
import TutorialsPage from './TutorialsPage.jsx';
import UsageDashboard from './UsageDashboard.jsx';
import Auth from './Auth.jsx';
import Affiliate from './Affiliate.jsx';
import { request, clearAuthKeys } from './lib.js';
import { hasAuthTokens, pageFromPath, requiresLogin } from './auth-session.js';
import { useAuthSession } from './useAuthSession.jsx';
import { useSiteFavicon } from './useSiteFavicon.jsx';
import { useTutorialConfig } from './useTutorialConfig.jsx';
import { Recharge, PaymentResult, StripePayment, AirwallexPayment } from './recharge.jsx';
import { AnnouncementBell, AnnouncementPopup, fetchAnnouncements, resetAnnouncements } from './announcements.jsx';
function getNav() {
  return [['home', tr("首页")], ['download', tr("软件下载")], ['tutorials', tr("使用教程")], ['keys', tr("API Key 管理")], ['models', tr("模型广场")], ['usage', tr("使用记录")], ['recharge', tr("充值")], ['affiliate', tr("邀请返利")]];
}
function dateOnly(value) {
  if (!value) return '—';
  const d = new Date(value);
  return Number.isNaN(d.getTime()) ? '—' : d.toLocaleDateString(getIntlLocale(), {
    year: 'numeric',
    month: '2-digit',
    day: '2-digit'
  });
}
function Logo() {
  return <img className="logo" src="/assets/yingzo-logo.png" alt="Yingzo" />;
}
function Toast({
  message,
  onClose
}) {
  return message ? <div className="toast" onClick={onClose}>{tr(message)}</div> : null;
}
function Shell({
  page,
  setPage,
  children,
  user,
  onLogout,
  tutorialsEnabled
}) {
  const visibleNav = getNav().filter(([id]) => id !== 'tutorials' || tutorialsEnabled);
  return <div className="app-shell"><aside><Logo /><LanguageSwitcher className="sidebar-language" /><div className="eyebrow">YINGZO / CREATIVE AGENT</div><nav>{visibleNav.map(([id, label]) => <button key={id} className={page === id ? 'active' : ''} onClick={() => {
          history.pushState({}, '', id === 'home' ? '/' : `/${id}`);
          setPage(id);
        }}>{label}</button>)}</nav>{user && <div className="bell-row"><AnnouncementBell /></div>}<div className="aside-note"><Trans i18nKey="让想法自然生长<0/><1>Stories · Images · Video</1>" components={[<br />, <small />]} /></div><button className="user-chip" onClick={() => setPage('profile')}><span>{user?.avatar_url ? <img src={user.avatar_url} alt="" /> : (user?.username || user?.name || user?.email || 'Y').slice(0, 1)}</span>{user?.username || user?.name || tr("创作者")}</button></aside><div className="shell-mobile-nav"><Logo /><LanguageSwitcher /><select aria-label={tr("工作区导航")} value={page} onChange={event => {
        const id = event.target.value;
        history.pushState({}, '', id === 'home' ? '/' : `/${id}`);
        setPage(id);
      }}>{[...visibleNav, ['profile', tr("个人资料")]].map(([id, label]) => <option key={id} value={id}>{label}</option>)}</select></div><main>{children}</main></div>;
}
function Page({
  title,
  desc,
  children
}) {
  return <><div className="page-head"><div><p className="kicker">YINGZO / WORKSPACE</p><h1>{title}</h1><p className="muted">{desc}</p></div></div>{children}</>;
}
function Keys() {
  const [keys, setKeys] = useState([]);
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [modal, setModal] = useState(null);
  const [name, setName] = useState('Yingzo Agent');
  const [agentGroupId, setAgentGroupId] = useState(null);
  const [error, setError] = useState('');
  const [toast, setToast] = useState('');
  useEffect(() => {
    Promise.all([request('/keys?page=1&page_size=10'), request('/groups/available')]).then(([keyData, groupData]) => {
      setKeys(Array.isArray(keyData) ? keyData : keyData?.items || []);
      const groups = Array.isArray(groupData) ? groupData : groupData?.items || [];
      const agent = groups.find(g => g.kind === 'agent' && g.system_code === 'yingzo') || groups.find(g => String(g.name || '').toLowerCase().includes('yingzo agent'));
      if (agent) setAgentGroupId(agent.id);else setError(message("未找到 Yingzo Agent 分组，无法创建 Key"));
    }).catch(err => setError(message("无法加载数据：{{value1}}", {
      value1: err.message
    }))).finally(() => setLoading(false));
  }, []);
  function openCreate() {
    setName('Yingzo Agent');
    setError('');
    setModal('create');
  }
  function openEdit(k) {
    setName(k.name || 'Yingzo Agent');
    setError('');
    setModal(k);
  }
  async function createKey() {
    const trimmed = name.trim();
    if (!trimmed) return setError(message("请输入 Key 名称"));
    if (!agentGroupId) return setError(message("未找到 Yingzo Agent 分组，无法创建 Key"));
    setBusy(true);
    try {
      const result = await request('/keys', {
        method: 'POST',
        body: JSON.stringify({
          name: trimmed,
          group_id: agentGroupId
        })
      });
      setKeys([result.data || result, ...keys]);
      setModal(null);
    } catch (err) {
      setError(err.message);
    } finally {
      setBusy(false);
    }
  }
  async function editKey() {
    const trimmed = name.trim();
    if (!trimmed) return setError(message("请输入 Key 名称"));
    setBusy(true);
    try {
      const result = await request(`/keys/${modal.id}`, {
        method: 'PUT',
        body: JSON.stringify({
          name: trimmed
        })
      });
      const updated = result.data || result;
      setKeys(keys.map(k => k.id === modal.id ? {
        ...k,
        ...updated
      } : k));
      setModal(null);
    } catch (err) {
      setError(err.message);
    } finally {
      setBusy(false);
    }
  }
  async function deleteKey() {
    setBusy(true);
    try {
      await request(`/keys/${modal.id}`, {
        method: 'DELETE'
      });
      setKeys(keys.filter(k => k.id !== modal.id));
      setModal(null);
      setToast(message("API Key 已删除"));
      setTimeout(() => setToast(''), 2400);
    } catch (err) {
      setError(err.message);
    } finally {
      setBusy(false);
    }
  }
  async function copyKey(k) {
    try {
      await navigator.clipboard.writeText(k.key);
      setToast(message("API Key 已复制"));
      setTimeout(() => setToast(''), 2400);
    } catch {
      setToast(message("复制失败，请检查浏览器权限"));
    }
  }
  return <Page title={tr("API Key 管理")} desc={tr("为 Yingzo Agent 创作工作区管理访问凭证。")}><div className="toolbar"><button className="dark" onClick={openCreate} disabled={busy}>{tr("创建 Key +")}</button></div><div className="panel"><div className="table-head key-head"><span>{tr("名称")}</span><span>Key</span><span>{tr("创建时间")}</span><span>{tr("状态")}</span><span>{tr("操作")}</span></div>{loading ? <div className="empty">{tr("正在加载…")}</div> : keys.length ? keys.map(k => <div className="table-row key-row" key={k.id}><span>{k.name || 'Yingzo Agent'}</span><span className="key-value"><span>yk_••••••••{String(k.id).slice(-4)}</span>{k.key && <button className="icon-button" title={tr("复制 API Key")} onClick={() => copyKey(k)}>{tr("复制")}</button>}</span><span>{dateOnly(k.created_at)}</span><span className="status">● {k.status === 'active' ? tr('Active') : tr('Inactive')}</span><span className="actions"><button onClick={() => openEdit(k)}>{tr("编辑")}</button><button className="danger" onClick={() => {
            setError('');
            setModal({
              type: 'delete',
              ...k
            });
          }}>{tr("删除")}</button></span></div>) : <div className="empty"><strong>{tr("还没有 API Key")}</strong><p>{tr("创建一个 Key，让 Yingzo Agent 连接你的创作工作区。")}</p><button className="dark" onClick={openCreate}>{tr("创建第一个 Key")}</button></div>}</div>{modal && <div className="modal-backdrop" onMouseDown={e => e.target === e.currentTarget && !busy && setModal(null)}><div className="modal-card">{modal === 'create' || modal?.id && modal.type !== 'delete' ? <><div className="modal-brand"><Logo /><span>YINGZO / WORKSPACE</span></div><h2>{modal === 'create' ? tr("创建 API Key") : tr("编辑 API Key 名称")}</h2><p className="muted">{modal === 'create' ? tr("此 Key 将固定绑定到 Yingzo Agent 分组。") : tr("修改名称不会影响 Key 本身。")}</p><label>{tr("Key 名称")}<input autoFocus value={name} onChange={e => setName(e.target.value)} onKeyDown={e => e.key === 'Enter' && (modal === 'create' ? createKey() : editKey())} placeholder={tr("例如：我的创作工作区")} /></label>{error && <div className="error">{tr(error)}</div>}<div className="modal-actions"><button className="outline" onClick={() => setModal(null)}>{tr("取消")}</button><button className="dark" onClick={modal === 'create' ? createKey : editKey} disabled={busy}>{busy ? tr("处理中…") : modal === 'create' ? tr("确认创建 →") : tr("保存 →")}</button></div></> : <><h2>{tr("确认删除 API Key？")}</h2><p className="muted"><Trans i18nKey="删除“<0/>”后将无法恢复，也无法再使用它访问工作区。" components={[<>{modal.name || 'Yingzo Agent'}</>]} /></p>{error && <div className="error">{tr(error)}</div>}<div className="modal-actions"><button className="outline" onClick={() => setModal(null)}>{tr("取消")}</button><button className="dark danger-button" onClick={deleteKey} disabled={busy}>{busy ? tr("删除中…") : tr("确认删除")}</button></div></>}</div></div>}<Toast message={tr(toast)} onClose={() => setToast('')} /></Page>;
}
function Usage() {
  return <Page title={tr("使用记录")} desc={tr("查看创作请求、媒体生成与余额消耗，金额单位为人民币。")}><UsageDashboard /></Page>;
}
function Profile({
  setUser,
  onLogout
}) {
  const [p, setP] = useState(null),
    [name, setName] = useState(''),
    [avatar, setAvatar] = useState(''),
    [pw, setPw] = useState({
      old: '',
      next: '',
      confirm: ''
    }),
    [msg, setMsg] = useState(''),
    [err, setErr] = useState('');
  useEffect(() => {
    request('/user/profile').then(x => {
      setP(x);
      setName(x.username || '');
      setAvatar(x.avatar_url || '');
    }).catch(e => setErr(e.message));
  }, []);
  function chooseAvatar(e) {
    const f = e.target.files?.[0];
    if (!f) return;
    if (!f.type.startsWith('image/')) return setErr(message("请选择图片文件"));
    const r = new FileReader();
    r.onload = () => setAvatar(String(r.result || ''));
    r.readAsDataURL(f);
  }
  async function save() {
    try {
      const x = await request('/user', {
        method: 'PUT',
        body: JSON.stringify({
          username: name,
          avatar_url: avatar || null
        })
      });
      setP(x);
      setUser?.(x);
      localStorage.setItem('auth_user', JSON.stringify(x));
      setMsg(message("资料已保存"));
    } catch (e) {
      setErr(e.message);
    }
  }
  async function change() {
    if (pw.next.length < 6 || pw.next !== pw.confirm) return setErr(message("新密码至少 6 位且两次输入必须一致"));
    try {
      await request('/user/password', {
        method: 'PUT',
        body: JSON.stringify({
          old_password: pw.old,
          new_password: pw.next
        })
      });
      setPw({
        old: '',
        next: '',
        confirm: ''
      });
      setMsg(message("密码已更新"));
    } catch (e) {
      setErr(e.message);
    }
  }
  return <Page title={tr("个人资料")} desc={tr("管理你的身份、偏好与安全设置。")}><div className="profile-actions-row"><button className="profile-logout" onClick={onLogout}>{tr("退出登录")}</button></div>{err && <div className="error">{tr(err)}</div>}<Toast message={tr(msg)} onClose={() => setMsg('')} /><div className="profile-summary panel"><label className="avatar avatar-edit" title={tr("点击更换头像")}><input type="file" accept="image/*" onChange={chooseAvatar} />{avatar ? <img src={avatar} alt={tr("头像")} /> : (p?.username || p?.email || 'U').slice(0, 1).toUpperCase()}<span>{tr("编辑")}</span></label><div><h2>{p?.username || p?.email || '—'}</h2><p className="muted">{p?.email}</p><span className="badge">{p?.role === 'admin' ? tr("管理员") : tr("创作者")}</span> <span className="badge">{p?.status === 'active' ? tr('Active') : p?.status ? tr('Inactive') : tr('启用')}</span></div><div className="profile-stats"><div><small>{tr("账户余额")}</small><strong>¥{formatNumber(Number(p?.balance || 0), {
              minimumFractionDigits: 2,
              maximumFractionDigits: 2
            })}</strong></div><div><small>{tr("并发限制")}</small><strong>{p?.concurrency ?? '—'}</strong></div><div><small>{tr("注册时间")}</small><strong>{p?.created_at ? new Date(p.created_at).toLocaleDateString(getIntlLocale()) : '—'}</strong></div></div></div><div className="form-grid"><div className="panel form-card"><h2>{tr("资料与头像")}</h2><p className="muted">{tr("点击上方头像即可上传新头像。")}</p><label>{tr("昵称")}<input value={name} onChange={e => setName(e.target.value)} placeholder={tr("你的昵称")} /></label><button className="dark" onClick={save}>{tr("保存资料 →")}</button></div><div className="panel form-card"><h2>{tr("更改密码")}</h2><label>{tr("当前密码")}<input type="password" value={pw.old} onChange={e => setPw({
            ...pw,
            old: e.target.value
          })} /></label><label>{tr("新密码")}<input type="password" value={pw.next} onChange={e => setPw({
            ...pw,
            next: e.target.value
          })} /></label><label>{tr("确认新密码")}<input type="password" value={pw.confirm} onChange={e => setPw({
            ...pw,
            confirm: e.target.value
          })} /></label><button className="dark" onClick={change}>{tr("更新密码 →")}</button></div></div></Page>;
}
export function App() {
  useSiteFavicon();
  const {
    i18n: language
  } = useTranslation();
  const [page, setPage] = useState(() => pageFromPath(location.pathname));
  const {
    user,
    status,
    setUser,
    retry
  } = useAuthSession();
  const {
    config: tutorialConfig,
    loading: tutorialsLoading
  } = useTutorialConfig(page);
  const tutorialsEnabled = tutorialConfig?.enabled === true;
  const authenticated = status === 'authenticated' && hasAuthTokens();
  const protectedPage = requiresLogin(page);
  const firstAuthRef = useRef(true);
  useEffect(() => {
    const title = {
      keys: 'API Key 管理',
      usage: '使用记录',
      profile: '个人资料',
      recharge: '充值',
      affiliate: '邀请返利',
      login: '登录',
      register: '创建账户',
      'forgot-password': '找回你的密码。',
      'reset-password': '重设密码'
    }[page];
    if (title) document.title = `${tr(title)} · Yingzo`;
  }, [page, language.language]);
  useEffect(() => {
    if (page === 'tutorials' && !tutorialsLoading && !tutorialsEnabled) {
      history.replaceState({}, '', '/');
      setPage('home');
    }
  }, [page, tutorialsLoading, tutorialsEnabled]);
  function logout() {
    clearAuthKeys();
    setUser(null);
    history.pushState({}, '', '/');
    setPage('home');
  }
  useEffect(() => {
    const onPop = () => setPage(pageFromPath(location.pathname));
    addEventListener('popstate', onPop);
    return () => removeEventListener('popstate', onPop);
  }, []);
  useEffect(() => {
    if (protectedPage && (status === 'anonymous' || status === 'authenticated' && !hasAuthTokens())) {
      history.replaceState({}, '', '/login');
      setPage('login');
    }
  }, [page, protectedPage, status]);

  // Announcements only run after the server has confirmed the session.
  useEffect(() => {
    if (authenticated) {
      if (firstAuthRef.current) {
        firstAuthRef.current = false;
        fetchAnnouncements(false);
      } else {
        const timer = setTimeout(() => fetchAnnouncements(true), 3000);
        return () => clearTimeout(timer);
      }
    } else {
      firstAuthRef.current = false;
      resetAnnouncements();
    }
  }, [authenticated]);
  useEffect(() => {
    if (authenticated) fetchAnnouncements(false);
  }, [page, authenticated]);
  useEffect(() => {
    const onVisible = () => {
      if (authenticated && document.visibilityState === 'visible') fetchAnnouncements(false);
    };
    document.addEventListener('visibilitychange', onVisible);
    return () => document.removeEventListener('visibilitychange', onVisible);
  }, [authenticated]);
  if (protectedPage && !authenticated) {
    if (status === 'error') return <div className="auth-page"><div className="result-card" role="alert"><h1>{tr("暂时无法确认登录状态")}</h1><p>{tr("请检查网络连接后重试。")}</p><button className="dark" onClick={retry}>{tr("重试")}</button></div></div>;
    if (status === 'checking') return <div className="auth-page" role="status">{tr("正在确认登录状态…")}</div>;
    return null;
  }
  if (page === 'payment/stripe') return <><LanguageSwitcher className="payment-language" /><StripePayment /><AnnouncementPopup /></>;
  if (page === 'payment/airwallex') return <><LanguageSwitcher className="payment-language" /><AirwallexPayment /><AnnouncementPopup /></>;
  if (page === 'payment/result' || page === 'recharge/result') return <><LanguageSwitcher className="payment-language" /><PaymentResult setUser={setUser} /><AnnouncementPopup /></>;
  if (['login', 'register', 'forgot-password', 'reset-password'].includes(page)) return <><Auth key={page} mode={page} setPage={setPage} setUser={setUser} /><AnnouncementPopup /></>;
  if (page === 'download') return <><DownloadPage setPage={setPage} user={user} /><AnnouncementPopup /></>;
  if (page === 'tutorials') {
    if (tutorialsLoading) return <div className="tutorial-empty" role="status">{tr("正在加载…")}</div>;
    if (!tutorialsEnabled) return null;
    return <><TutorialsPage config={tutorialConfig} setPage={setPage} user={authenticated ? user : null} /><AnnouncementPopup /></>;
  }
  if (page === 'home') return <><LandingPage setPage={setPage} user={user} tutorialsEnabled={tutorialsEnabled} /><AnnouncementPopup /></>;
  return <><Shell page={page} setPage={setPage} user={user} onLogout={logout} tutorialsEnabled={tutorialsEnabled}>
    {page === 'affiliate' ? <Affiliate setUser={setUser} /> : page === 'keys' ? <Keys /> : page === 'models' ? <ModelPlaza /> : page === 'usage' ? <Usage /> : page === 'profile' ? <Profile setUser={setUser} onLogout={logout} /> : <Recharge user={user} setUser={setUser} />}
  </Shell><AnnouncementPopup /></>;
}
