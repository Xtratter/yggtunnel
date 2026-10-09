function(ygg_test name)
    add_executable(${name} ${name}.cpp ${ARGN})
    target_link_libraries(${name} PRIVATE ygg_core Qt6::Test Qt6::Network)
    target_include_directories(${name} PRIVATE ${CMAKE_CURRENT_SOURCE_DIR} ${CMAKE_SOURCE_DIR}/src)
    add_test(NAME ${name} COMMAND ${name})
    set_tests_properties(${name} PROPERTIES ENVIRONMENT "QT_QPA_PLATFORM=offscreen;QT_QUICK_BACKEND=software")
endfunction()

ygg_test(tst_protocol)
ygg_test(tst_daemonclient fakedaemon.cpp)
ygg_test(tst_controller fakedaemon.cpp)

ygg_test(tst_window fakedaemon.cpp)
target_link_libraries(tst_window PRIVATE Qt6::Quick Qt6::QuickControls2)
target_compile_definitions(tst_window PRIVATE
    YGG_QML_DIR="${CMAKE_SOURCE_DIR}/qml" SHOTS_DIR="${CMAKE_BINARY_DIR}/shots")
