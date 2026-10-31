cmake_minimum_required(VERSION 3.20)
project(@NAME@ C ASM)
set(CMAKE_C_STANDARD 11)

set(CORE "$ENV{CMSIS_CORE_PATH}/CMSIS/Core/Include")
set(DEVICE "$ENV{STM32_DEVICE_PATH}")
set(HAL "$ENV{STM32_HAL_PATH}")
foreach(required "${CORE}/core_cm3.h" "${DEVICE}/Include/stm32f103xb.h" "${HAL}/Src/stm32f1xx_hal.c")
    if(NOT EXISTS "${required}")
        message(FATAL_ERROR "Missing pinned STM32 SDK file: ${required}. Run through slepok.")
    endif()
endforeach()

add_executable(@NAME@
    main.c
    runtime.c
    "${DEVICE}/Source/Templates/gcc/startup_stm32f103xb.s"
    "${DEVICE}/Source/Templates/system_stm32f1xx.c"
    "${HAL}/Src/stm32f1xx_hal.c"
    "${HAL}/Src/stm32f1xx_hal_cortex.c"
    "${HAL}/Src/stm32f1xx_hal_gpio.c"
    "${HAL}/Src/stm32f1xx_hal_rcc.c"
    "${HAL}/Src/stm32f1xx_hal_rcc_ex.c"
    "${HAL}/Src/stm32f1xx_hal_flash.c"
    "${HAL}/Src/stm32f1xx_hal_flash_ex.c"
    "${HAL}/Src/stm32f1xx_hal_pwr.c"
)
set_target_properties(@NAME@ PROPERTIES SUFFIX ".elf")
target_include_directories(@NAME@ PRIVATE "${CORE}" "${DEVICE}/Include" "${HAL}/Inc" "${CMAKE_CURRENT_SOURCE_DIR}")
target_compile_definitions(@NAME@ PRIVATE STM32F103xB USE_HAL_DRIVER)
target_compile_options(@NAME@ PRIVATE -mcpu=cortex-m3 -mthumb -ffunction-sections -fdata-sections -Wall -Wextra)
set(linker_script "${CMAKE_CURRENT_SOURCE_DIR}/stm32f103c8.ld")
target_link_options(@NAME@ PRIVATE -mcpu=cortex-m3 -mthumb -nostartfiles --specs=nano.specs --specs=nosys.specs
    "-T${linker_script}" -Wl,--gc-sections "-Wl,-Map=${CMAKE_CURRENT_BINARY_DIR}/@NAME@.map")
set_property(TARGET @NAME@ APPEND PROPERTY LINK_DEPENDS "${linker_script}")
add_custom_command(TARGET @NAME@ POST_BUILD
    COMMAND "${CMAKE_OBJCOPY}" -O binary "$<TARGET_FILE:@NAME@>" "${CMAKE_CURRENT_BINARY_DIR}/@NAME@.bin"
    COMMAND "${CMAKE_OBJCOPY}" -O ihex "$<TARGET_FILE:@NAME@>" "${CMAKE_CURRENT_BINARY_DIR}/@NAME@.hex"
    VERBATIM)
