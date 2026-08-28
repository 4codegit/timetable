import './style.css'
import { mount } from 'svelte'
import App from './App.svelte'

function showError(msg: string) {
  const el = document.getElementById('app');
  if (el) el.textContent = 'Startup error: ' + msg;
}

window.addEventListener('error', (e: any) => {
  showError(e.error ? e.error.message : e.message);
});
window.addEventListener('unhandledrejection', (e: any) => {
  const r = e.reason;
  showError(r ? (r.message || String(r)) : 'unknown');
});

let app: any;
try {
  app = mount(App, { target: document.getElementById('app') as HTMLElement });
} catch (e: any) {
  showError(e && e.message ? e.message : String(e));
}

export default app
