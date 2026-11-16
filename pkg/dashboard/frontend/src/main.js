import './styles/tokens.css';
import './styles/base.css';
import './styles/typography.css';
import './styles/components.css';
import './styles/figure.css';
import { mount } from 'svelte';
import App from './App.svelte';
import { takeSignInLink } from './lib/signInLink.ts';

// Before the hash router reads the URL: a #token= sign-in link leaves it first.
takeSignInLink();

if (globalThis.__PACTO_STATIC__ && !location.hash) {
  location.hash = `#/services/${encodeURIComponent(globalThis.__PACTO_STATIC__.service)}`;
}

mount(App, { target: document.getElementById('app') });
