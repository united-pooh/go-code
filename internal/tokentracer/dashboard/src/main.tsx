import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { GlobalApp } from './global/GlobalApp';
import 'dockview-react/dist/styles/dockview.css';
import './styles/tokens.css';
import './styles/app.css';
import './styles/dockview.css';
import './styles/panels.css';
import './styles/global.css';

createRoot(document.getElementById('root')!).render(
  <StrictMode><GlobalApp /></StrictMode>
);
