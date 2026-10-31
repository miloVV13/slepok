#include "stm32f1xx_hal.h"

/* Blue Pill PC13 LED is active-low. Use reset-default HSI at 8 MHz,
 * so the example does not depend on an external crystal or PLL setup. */
int main(void) {
    HAL_Init();
    __HAL_RCC_GPIOC_CLK_ENABLE();
    HAL_GPIO_WritePin(GPIOC, GPIO_PIN_13, GPIO_PIN_SET);
    GPIO_InitTypeDef led = {0};
    led.Pin = GPIO_PIN_13;
    led.Mode = GPIO_MODE_OUTPUT_PP;
    led.Speed = GPIO_SPEED_FREQ_LOW;
    HAL_GPIO_Init(GPIOC, &led);
    for (;;) {
        HAL_GPIO_TogglePin(GPIOC, GPIO_PIN_13);
        HAL_Delay(500);
    }
}

void SysTick_Handler(void) { HAL_IncTick(); }
