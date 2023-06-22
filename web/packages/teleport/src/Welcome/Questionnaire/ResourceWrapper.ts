import styled from 'styled-components';
import { Flex } from 'design';

export const ResourceWrapper = styled(Flex)`
  flex-direction: column;
  height: 100%;
  background-color: ${props => props.theme.colors.levels.surface};
  padding: 12px 0;
  gap: 16px;
  border-radius: 4px;

  &:hover {
    background-color: ${props => props.theme.colors.spotBackground[0]};
  }

  border: ${({ isSelected, invalid, theme }) => {
    if (isSelected) {
      return `1px solid ${theme.colors.brand}`;
    }
    if (invalid) {
      return `1px solid ${theme.colors.error.main}`;
    }
    return `1px solid ${theme.colors.levels.elevated}`;
  }};
`;
