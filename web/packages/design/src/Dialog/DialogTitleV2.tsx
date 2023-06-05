import React, { useMemo } from 'react';

import Text from 'design/Text';

export type DialogTitleV2Props = {
  title: string;
};

export const DialogTitleV2 = ({ title }: DialogTitleV2Props) => {
  const titleCase = useMemo(() => translateToTitleCase(title), [title]);

  return (
    <Text typography="h3" color="text.main" data-testid="title-text">
      {titleCase}
    </Text>
  );
};

/**
 * Returns a string in Title Case.
 * @example
 * Here's a simple example:
 * ```
 * // Prints "Hello Dear World!":
 * console.log(translateToTitleCase('hello dear world!'));
 * ```
 *
 * @remarks
 * Where Title Case converts the first element of all the words in a sentence in to uppercase while the other elements are lowercase
 *
 * @param message - the string to convert
 * @returns a Title Case string
 *
 */
const translateToTitleCase = (message: string): string => {
  return message
    .toLowerCase()
    .split(' ')
    .map(word => {
      return word.charAt(0).toUpperCase() + word.slice(1);
    })
    .join(' ');
};
