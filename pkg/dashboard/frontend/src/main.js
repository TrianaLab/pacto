import './styles/tokens.css';
import './styles/base.css';
import './styles/components.css';
import { mount } from 'svelte';
import App from './App.svelte';

if (globalThis.__PACTO_STATIC__ && !location.hash) {
  location.hash = `#/services/${encodeURIComponent(globalThis.__PACTO_STATIC__.service)}`;
}

mount(App, { target: document.getElementById('app') });
