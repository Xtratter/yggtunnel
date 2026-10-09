cmake_minimum_required(VERSION 3.21)
file(STRINGS ${CMAKE_CURRENT_SOURCE_DIR}/../internal/version/VERSION YGG_VERSION LIMIT_COUNT 1)
project(yggtunnel-gui VERSION ${YGG_VERSION} LANGUAGES CXX)

set(CMAKE_CXX_STANDARD 20)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_AUTOMOC ON)
find_package(Qt6 6.5 REQUIRED COMPONENTS Core Network Gui Quick QuickControls2 Test LinguistTools)
qt_standard_project_setup()

add_library(ygg_core STATIC src/protocol.cpp src/daemonclient.cpp src/controller.cpp)
target_include_directories(ygg_core PUBLIC src)
target_link_libraries(ygg_core PUBLIC Qt6::Core Qt6::Network)

add_executable(yggtunnel-gui src/main.cpp)
qt_add_resources(yggtunnel-gui qml PREFIX "/qml" BASE qml
    FILES qml/Main.qml qml/StatusCard.qml qml/Island.qml qml/ActionButton.qml qml/PeersCard.qml qml/ProtectionCard.qml qml/RoutingCard.qml qml/ImportDialog.qml qml/LogPage.qml)
qt_add_translations(yggtunnel-gui TS_FILES translations/yggtunnel_ru.ts
    RESOURCE_PREFIX /i18n NO_GENERATE_PLURALS_TS_FILE)
target_link_libraries(yggtunnel-gui PRIVATE ygg_core Qt6::Gui Qt6::Quick Qt6::QuickControls2)
target_compile_definitions(yggtunnel-gui PRIVATE YGG_VERSION="${PROJECT_VERSION}")

enable_testing()
get_target_property(_lupdate Qt6::lupdate IMPORTED_LOCATION)
add_test(NAME translations COMMAND ${CMAKE_COMMAND} -DLUPDATE=${_lupdate} -DSRC=${CMAKE_CURRENT_SOURCE_DIR}
         -DWORK=${CMAKE_BINARY_DIR}/i18n-check -P ${CMAKE_CURRENT_SOURCE_DIR}/tests/check_translations.cmake)
add_subdirectory(tests)

include(GNUInstallDirs)
install(TARGETS yggtunnel-gui RUNTIME DESTINATION ${CMAKE_INSTALL_BINDIR})
install(FILES ../packaging/yggtunnel-gui.desktop DESTINATION ${CMAKE_INSTALL_DATADIR}/applications)
install(FILES icons/yggtunnel.png DESTINATION ${CMAKE_INSTALL_DATADIR}/icons/hicolor/512x512/apps)
