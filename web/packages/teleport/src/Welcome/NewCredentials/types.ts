import { Attempt } from 'shared/hooks/useAttemptNext';

import {Auth2faType, PrimaryAuthType} from 'shared/services';

import { RecoveryCodes, ResetToken } from 'teleport/services/auth';

export type UseTokenState = {
  auth2faType: Auth2faType;
  primaryAuthType: PrimaryAuthType;
  isPasswordlessEnabled: boolean;
  fetchAttempt: Attempt;
  submitAttempt: Attempt;
  clearSubmitAttempt: () => void;
  onSubmit: (password: string, otpCode?: string, deviceName?: string) => void;
  onSubmitWithWebauthn: (password?: string, deviceName?: string) => void;
  resetToken: ResetToken;
  recoveryCodes: RecoveryCodes;
  redirect: () => void;
  success: boolean;
  finishedRegister: () => void;
  privateKeyPolicyEnabled: boolean;
  displayOnboardingQuestionnaire: boolean;
  setDisplayOnboardingQuestionnaire: (bool: boolean) => void;
};

export type NewCredentialsProps = UseTokenState & {
  resetMode?: boolean;
};

export type RegisterSuccessProps = {
  redirect(): void;
  resetMode: boolean;
  username?: string;
};
