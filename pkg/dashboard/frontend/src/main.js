import './styles/tokens.css';
import './styles/base.css';
import './styles/components.css';
import { mount } from 'svelte';
import App from './App.svelte';

mount(App, { target: document.getElementById('app') });
