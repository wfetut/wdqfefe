/*
Copyright 2019-2021 Gravitational, Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

import React, { lazy, Suspense } from 'react';
import ThemeProvider from 'design/ThemeProvider';

import { Route, Router, Switch } from 'teleport/components/Router';
import { CatchError } from 'teleport/components/CatchError';
import Authenticated from 'teleport/components/Authenticated';

import { getOSSFeatures } from 'teleport/features';

import { LayoutContextProvider } from 'teleport/Main/LayoutContext';
import { UserContextProvider } from 'teleport/User';

import TeleportContextProvider from './TeleportContextProvider';
import TeleportContext from './teleportContext';
import cfg from './config';

import type { History } from 'history';

const AppLauncher = lazy(() => import('./AppLauncher'));

const Teleport: React.FC<Props> = props => {
  const { ctx, history } = props;
  const createPublicRoutes = props.renderPublicRoutes || publicOSSRoutes;
  const createPrivateRoutes = props.renderPrivateRoutes || privateOSSRoutes;

  return (
    <CatchError>
      <ThemeProvider>
        <LayoutContextProvider>
          <Router history={history}>
            <Suspense fallback={null}>
              <Switch>
                {createPublicRoutes()}
                <Route path={cfg.routes.root}>
                  <Authenticated>
                    <UserContextProvider>
                      <TeleportContextProvider ctx={ctx}>
                        <Switch>
                          <Route
                            path={cfg.routes.appLauncher}
                            component={AppLauncher}
                          />
                          <Route>{createPrivateRoutes()}</Route>
                        </Switch>
                      </TeleportContextProvider>
                    </UserContextProvider>
                  </Authenticated>
                </Route>
              </Switch>
            </Suspense>
          </Router>
        </LayoutContextProvider>
      </ThemeProvider>
    </CatchError>
  );
};

const LoginFailed = lazy(() => import('./Login/LoginFailed'));
const LoginSuccess = lazy(() => import('./Login/LoginSuccess'));
const Login = lazy(() => import('./Login'));
const Welcome = lazy(() => import('./Welcome'));

const Console = lazy(() => import('./Console'));
const Player = lazy(() => import('./Player'));
const DesktopSession = lazy(() => import('./DesktopSession'));

const HeadlessRequest = lazy(() => import('./HeadlessRequest'));

const Main = lazy(() => import('./Main'));

function publicOSSRoutes() {
  return [
    <Route
      title="Login"
      path={cfg.routes.login}
      component={Login}
      key="login"
    />,
    ...getSharedPublicRoutes(),
  ];
}

export function getSharedPublicRoutes() {
  return [
    <Route
      key="login-failed"
      title="Login Failed"
      path={cfg.routes.loginError}
      component={LoginFailed}
    />,
    <Route
      key="login-failed-legacy"
      title="Login Failed"
      path={cfg.routes.loginErrorLegacy}
      component={LoginFailed}
    />,
    <Route
      key="success"
      title="Success"
      path={cfg.routes.loginSuccess}
      component={LoginSuccess}
    />,
    <Route
      key="invite"
      title="Invite"
      path={cfg.routes.userInvite}
      component={Welcome}
    />,
    <Route
      key="password-reset"
      title="Password Reset"
      path={cfg.routes.userReset}
      component={Welcome}
    />,
  ];
}

function privateOSSRoutes() {
  return (
    <Switch>
      {getSharedPrivateRoutes()}
      <Route
        path={cfg.routes.root}
        render={() => <Main features={getOSSFeatures()} />}
      />
    </Switch>
  );
}

export function getSharedPrivateRoutes() {
  return [
    <Route
      key="desktop"
      path={cfg.routes.desktop}
      component={DesktopSession}
    />,
    <Route key="console" path={cfg.routes.console} component={Console} />,
    <Route key="player" path={cfg.routes.player} component={Player} />,
    <Route
      key="headlessSSO"
      path={cfg.routes.headlessSso}
      component={HeadlessRequest}
    />,
  ];
}

export default Teleport;

export type Props = {
  ctx: TeleportContext;
  history: History;
  renderPublicRoutes?: () => React.ReactNode[];
  renderPrivateRoutes?: () => React.ReactNode;
};
